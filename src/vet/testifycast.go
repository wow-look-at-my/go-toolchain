package vet

import (
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
	"golang.org/x/tools/go/analysis"
)

// Upstream testify package paths. Calls resolve here when the toolchain
// rewrites the fork back to upstream, before analysis runs.
const (
	upstreamAssertPkg  = "github.com/stretchr/testify/assert"
	upstreamRequirePkg = "github.com/stretchr/testify/require"
)

// TestifyCastAnalyzer inserts conversions into testify Equal/NotEqual and
// Greater/Less assertions so code written against the loosened fork still
// passes upstream. Fixes are surgical byte edits (CastEdits), not reprints.
var TestifyCastAnalyzer = &analysis.Analyzer{
	Name:       "testifycast",
	Doc:        "inserts type conversions for cross-type testify Equal/NotEqual and Greater/Less-family assertions so they pass against upstream testify",
	Run:        runTestifyCast,
	ResultType: reflect.TypeOf([]*CastEdits{}),
}

// CastEdit wraps [Start,End) in TypeName(...). AddImports lists paths needed
// to resolve TypeName when not already imported.
type CastEdit struct {
	Start, End token.Pos
	TypeName   string
	AddImports []string
}

// CastEdits is the set of conversions to apply to a file.
type CastEdits struct {
	Filename string
	Fset     *token.FileSet
	Edits    []CastEdit
}

// equalityFuncs are the fork-loosened assertions rewritten with a conversion.
var equalityFuncs = set.Of(
	"Equal", "Equalf",
	"NotEqual", "NotEqualf",
)

// orderingFuncs are the ordering assertions; upstream needs matching
// reflect kinds, so they share equalityFuncs' conversion treatment.
var orderingFuncs = set.Of(
	"Greater", "Greaterf",
	"GreaterOrEqual", "GreaterOrEqualf",
	"Less", "Lessf",
	"LessOrEqual", "LessOrEqualf",
)

// elementFuncs need collection element-type reasoning to rewrite soundly, so
// we only warn on a type-mismatched element comparison instead.
var elementFuncs = set.Of(
	"Contains", "Containsf",
	"NotContains", "NotContainsf",
	"ElementsMatch", "ElementsMatchf",
	"Subset", "Subsetf",
	"NotSubset", "NotSubsetf",
)

func runTestifyCast(pass *analysis.Pass) (any, error) {
	fileToEdits := make(map[*ast.File]*CastEdits)

	for _, file := range pass.Files {
		// Cheap pre-scan: skip files that don't import upstream testify at all.
		if !importsUpstreamTestify(file) {
			continue
		}
		filename := pass.Fset.File(file.Pos()).Name()

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			fn, ok := pass.TypesInfo.ObjectOf(sel.Sel).(*types.Func)
			if !ok || fn.Pkg() == nil {
				return true
			}
			pkgPath := fn.Pkg().Path()
			if pkgPath != upstreamAssertPkg && pkgPath != upstreamRequirePkg {
				return true
			}

			name := fn.Name()
			expIdx, actIdx, ok := equalArgIndices(fn, call)
			if !ok {
				return true
			}

			switch {
			case equalityFuncs.Contains(name) || orderingFuncs.Contains(name):
				if edit := castEditForEqual(pass, file, call, expIdx, actIdx); edit != nil {
					ce := fileToEdits[file]
					if ce == nil {
						ce = &CastEdits{Filename: filename, Fset: pass.Fset}
						fileToEdits[file] = ce
					}
					ce.Edits = append(ce.Edits, *edit)
				}
			case elementFuncs.Contains(name):
				warnElementMismatch(pass, call, name, expIdx, actIdx)
			}
			return true
		})
	}

	if len(fileToEdits) == 0 {
		return []*CastEdits(nil), nil
	}

	var result []*CastEdits
	for _, ce := range fileToEdits {
		result = append(result, ce)
	}
	return result, nil
}

// equalArgIndices returns the argument indices of the compared operands for
// a testify comparison assertion, accounting for the package form (which takes
// a leading TestingT) versus the *Assertions method form (which does not).
// The boolean is false if the call doesn't have enough arguments.
func equalArgIndices(fn *types.Func, call *ast.CallExpr) (exp, act int, ok bool) {
	sig, isSig := fn.Type().(*types.Signature)
	if !isSig {
		return 0, 0, false
	}
	if sig.Recv() != nil {
		// (*assert.Assertions).Equal(expected, actual, ...)
		exp, act = 0, 1
	} else {
		// assert.Equal(t, expected, actual, ...)
		exp, act = 1, 2
	}
	if len(call.Args) <= act {
		return 0, 0, false
	}
	return exp, act, true
}

// castEditForEqual decides whether a conversion is needed to make the
// operands of an Equal/NotEqual or ordering assertion the same static type, and if so
// returns the edit wrapping the chosen operand. It returns nil when no sound
// conversion applies (identical types, non-convertible, fractional-truncating
// constants, etc.), in which case the assertion is left exactly as the fork
// would have evaluated it.
func castEditForEqual(pass *analysis.Pass, file *ast.File, call *ast.CallExpr, expIdx, actIdx int) *CastEdit {
	expExpr := call.Args[expIdx]
	actExpr := call.Args[actIdx]

	expTV, ok1 := pass.TypesInfo.Types[expExpr]
	actTV, ok2 := pass.TypesInfo.Types[actExpr]
	if !ok1 || !ok2 || expTV.Type == nil || actTV.Type == nil {
		return nil
	}

	expType := types.Default(expTV.Type)
	actType := types.Default(actTV.Type)

	// Identical types need no edit (keeps the pass idempotent).
	if types.Identical(expType, actType) {
		return nil
	}

	expBasic, expIsBasic := expType.Underlying().(*types.Basic)
	actBasic, actIsBasic := actType.Underlying().(*types.Basic)
	if !expIsBasic || !actIsBasic {
		// Not a basic-kinded value; the fork only loosened same-kind numeric basics.
		return nil
	}

	expNumeric := isForkNumeric(expBasic)
	actNumeric := isForkNumeric(actBasic)

	switch {
	case expNumeric && actNumeric:
		// Numeric mismatch; convert the literal side, else convert
		// expected to actual's type to keep the tested value visible.
		if isUntypedLiteral(actExpr) && !isUntypedLiteral(expExpr) {
			return buildCastEdit(pass, file, actExpr, actTV, expType)
		}
		return buildCastEdit(pass, file, expExpr, expTV, actType)

	case expNumeric != actNumeric:
		// Mismatched numeric-ness compared false in the fork; no sound cast.
		return nil

	default:
		// Neither numeric. Mirror the fork: convert only same-Kind()
		// convertible named types (e.g. type Name string vs string).
		if expBasic.Kind() != actBasic.Kind() {
			return nil
		}
		if !types.ConvertibleTo(expType, actType) {
			return nil
		}
		return buildCastEdit(pass, file, expExpr, expTV, actType)
	}
}

// buildCastEdit constructs the edit that wraps argExpr (whose type-and-value is
// argTV) in a conversion to target. It returns nil when the conversion would be
// unsound — for constant operands, when the constant isn't representable in the
// target type (fractional truncation or overflow), mirroring the fork, which
// compares the original numeric values and would not have considered such a
// pair equal.
func buildCastEdit(pass *analysis.Pass, file *ast.File, argExpr ast.Expr, argTV types.TypeAndValue, target types.Type) *CastEdit {
	// Guard numeric constants against value-changing conversions (truncation or
	// overflow); non-numeric conversions are always representable.
	if argTV.Value != nil {
		if tb, ok := target.Underlying().(*types.Basic); ok && tb.Info()&types.IsNumeric != 0 {
			if !constRepresentable(argTV.Value, tb) {
				return nil
			}
		}
	}

	base := fileQualifier(pass.Pkg, file)
	used := make(map[*types.Package]string)
	name := types.TypeString(target, func(p *types.Package) string {
		q := base(p)
		if p != nil && q != "" {
			used[p] = q
		}
		return q
	})
	if name == "" || strings.ContainsAny(name, " \t\n") || strings.Contains(name, "invalid") {
		// Skip rather than emit a conversion spelling that would not compile.
		return nil
	}

	// Verify each package name resolves at the use site; record missing imports (e.g. os.FileMode aliases io/fs.FileMode).
	var addImports []string
	for p, local := range used {
		switch obj := lookupAt(pass, argExpr.Pos(), local).(type) {
		case *types.PkgName:
			if obj.Imported().Path() != p.Path() {
				// Taken by a different import; adding an alias cannot make this spelling resolve.
				return nil
			}
			// Already imported under this name: nothing to add.
		case nil:
			// Not in scope: adding the import resolves it only when local is the bare
			// package name the conversion uses.
			if local != p.Name() {
				return nil
			}
			addImports = append(addImports, p.Path())
		default:
			// Shadowed by a non-package identifier here; the conversion cannot resolve.
			return nil
		}
	}

	return &CastEdit{
		Start:      argExpr.Pos(),
		End:        argExpr.End(),
		TypeName:   name,
		AddImports: addImports,
	}
}

// lookupAt returns the object that name resolves to at pos within pass's
// package, or nil when the name is not in scope there.
func lookupAt(pass *analysis.Pass, pos token.Pos, name string) types.Object {
	scope := pass.Pkg.Scope().Innermost(pos)
	if scope == nil {
		return nil
	}
	_, obj := scope.LookupParent(name, pos)
	return obj
}

// fileQualifier returns a types.Qualifier that names packages using the import
// alias in effect within file, and the empty string for the file's own package.
func fileQualifier(self *types.Package, file *ast.File) types.Qualifier {
	return func(p *types.Package) string {
		if p == nil || p == self {
			return ""
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if path != p.Path() {
				continue
			}
			if imp.Name != nil && imp.Name.Name != "_" {
				if imp.Name.Name == "." {
					// Dot import: identifiers are in file scope, so spell unqualified (Duration, not .Duration).
					return ""
				}
				return imp.Name.Name
			}
			return p.Name()
		}
		// Not imported here; fall back to the package name (buildCastEdit records the import to add if this spelling doesn't resolve).
		return p.Name()
	}
}

// isForkNumeric reports whether a basic type is numeric in the sense the fork's
// isNumericType used (reflect kinds Int..Complex128), excluding complex: the
// fork's numeric comparison path (toFloat64) does not handle complex, and
// constant casts into complex are out of scope.
func isForkNumeric(b *types.Basic) bool {
	switch b.Kind() {
	case types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
		types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64, types.Uintptr,
		types.Float32, types.Float64:
		return true
	}
	return false
}

// isUntypedLiteral reports a bare literal expr. go/types loses untyped-ness
// on interface{} args, so AST shape decides instead.
func isUntypedLiteral(expr ast.Expr) bool {
	_, ok := expr.(*ast.BasicLit)
	return ok
}
