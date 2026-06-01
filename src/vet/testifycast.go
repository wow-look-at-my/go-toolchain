package vet

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/types"
	"math"
	"os"
	"reflect"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// Upstream testify package paths. The toolchain rewrites the in-house fork
// (github.com/wow-look-at-my/testify) back to these before analysis runs, so by
// the time this analyzer executes the calls resolve to upstream.
const (
	upstreamAssertPkg  = "github.com/stretchr/testify/assert"
	upstreamRequirePkg = "github.com/stretchr/testify/require"
)

// TestifyCastAnalyzer inserts explicit type conversions into the arguments of
// upstream testify equality assertions (assert/require .Equal/.NotEqual and
// their f-variants, in both package and *Assertions method form).
//
// The wow-look-at-my/testify fork loosened ObjectsAreEqual so that
// numerically-equal values of different convertible types compared equal
// (e.g. assert.Equal(t, 0, f) with f float64). Upstream testify only does
// reflect.DeepEqual, which is type-strict. To keep code that relied on the
// fork compiling and passing against upstream, this analyzer makes the two
// compared operands the same static type by wrapping one of them in a
// conversion — mirroring exactly the cases ObjectsAreEqual would have treated
// as equal.
var TestifyCastAnalyzer = &analysis.Analyzer{
	Name:       "testifycast",
	Doc:        "inserts type conversions for cross-type testify Equal/NotEqual assertions so they pass against upstream testify",
	Run:        runTestifyCast,
	ResultType: reflect.TypeOf([]*ASTFixes{}),
}

// equalityFuncs are the testify assertions whose semantics changed in the fork
// because they route argument comparison through ObjectsAreEqual. These are the
// ones we rewrite with a conversion.
var equalityFuncs = map[string]bool{
	"Equal": true, "Equalf": true,
	"NotEqual": true, "NotEqualf": true,
}

// elementFuncs are testify assertions whose element comparison also uses
// ObjectsAreEqual. Rewriting these soundly is much harder (it requires
// reasoning about collection element types), so we only warn when we detect a
// type-mismatched element comparison rather than silently changing behavior.
var elementFuncs = map[string]bool{
	"Contains": true, "Containsf": true,
	"NotContains": true, "NotContainsf": true,
	"ElementsMatch": true, "ElementsMatchf": true,
	"Subset": true, "Subsetf": true,
	"NotSubset": true, "NotSubsetf": true,
}

func runTestifyCast(pass *analysis.Pass) (any, error) {
	fileToFixes := make(map[*ast.File][]ASTFix)

	for _, file := range pass.Files {
		// Cheap pre-scan: skip files that don't import upstream testify at all.
		if !importsUpstreamTestify(file) {
			continue
		}

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
			case equalityFuncs[name]:
				if fix := castFixForEqual(pass, file, call, expIdx, actIdx); fix != nil {
					fileToFixes[file] = append(fileToFixes[file], *fix)
				}
			case elementFuncs[name]:
				warnElementMismatch(pass, call, name, expIdx, actIdx)
			}
			return true
		})
	}

	if len(fileToFixes) == 0 {
		return []*ASTFixes(nil), nil
	}

	var result []*ASTFixes
	for file, fixes := range fileToFixes {
		result = append(result, &ASTFixes{
			File:  file,
			Fset:  pass.Fset,
			Fixes: fixes,
		})
	}
	return result, nil
}

// equalArgIndices returns the argument indices of the two compared operands for
// a testify two-operand assertion, accounting for the package form (which takes
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

// castFixForEqual decides whether a conversion is needed to make the two
// operands of an Equal/NotEqual assertion the same static type, and if so
// returns an ASTFix wrapping the chosen operand. It returns nil when no sound
// conversion applies (identical types, non-convertible, fractional-truncating
// constants, etc.), in which case the assertion is left exactly as the fork
// would have evaluated it.
func castFixForEqual(pass *analysis.Pass, file *ast.File, call *ast.CallExpr, expIdx, actIdx int) *ASTFix {
	expExpr := call.Args[expIdx]
	actExpr := call.Args[actIdx]

	expTV, ok1 := pass.TypesInfo.Types[expExpr]
	actTV, ok2 := pass.TypesInfo.Types[actExpr]
	if !ok1 || !ok2 || expTV.Type == nil || actTV.Type == nil {
		return nil
	}

	expType := types.Default(expTV.Type)
	actType := types.Default(actTV.Type)

	// Rule 1: identical static types -> nothing to do (also makes the pass
	// idempotent: an already-inserted conversion makes the types identical).
	if types.Identical(expType, actType) {
		return nil
	}

	expBasic, expIsBasic := expType.Underlying().(*types.Basic)
	actBasic, actIsBasic := actType.Underlying().(*types.Basic)
	if !expIsBasic || !actIsBasic {
		// At least one operand isn't a basic-kinded value. The fork only
		// loosened convertible same-kind / numeric basics; leave others alone.
		return nil
	}

	// Rule 6: the fork returns false when types aren't convertible. Leave such
	// assertions to fail under upstream exactly as they failed under the fork.
	expNumeric := isForkNumeric(expBasic)
	actNumeric := isForkNumeric(actBasic)

	switch {
	case expNumeric && actNumeric:
		// Rule 3: numeric mismatch. Pick which side to convert.
		// If the actual is an untyped literal and the expected is not, convert
		// the actual (the literal) to the expected's type; otherwise convert
		// the expected to the actual's type (keeps the value-under-test visible).
		if isUntypedLiteral(actExpr) && !isUntypedLiteral(expExpr) {
			return buildCastFix(pass, file, call, actExpr, actTV, expType)
		}
		return buildCastFix(pass, file, call, expExpr, expTV, actType)

	case expNumeric != actNumeric:
		// One numeric, one not (e.g. string vs []byte handled elsewhere): the
		// fork required matching kinds, so a numeric-vs-nonnumeric pair with
		// different kinds compared false. No sound cast. Rule 2.
		return nil

	default:
		// Rule 5: neither numeric. The fork converts only when Kind() matches
		// and the types are convertible, then DeepEqual. Mirror that for
		// same-kind convertible named types (e.g. type Celsius float64 vs
		// float64). []byte vs []byte is already handled by upstream (bytes.Equal)
		// and is caught by the identical-types check above on its element basis.
		if expBasic.Kind() != actBasic.Kind() {
			return nil
		}
		if !types.ConvertibleTo(expType, actType) {
			return nil
		}
		return buildCastFix(pass, file, call, expExpr, expTV, actType)
	}
}

// buildCastFix constructs the ASTFix that wraps argExpr (whose type-and-value is
// argTV) in a conversion to target. It returns nil when the conversion would be
// unsound — for constant operands, when the constant isn't representable in the
// target type (fractional truncation or overflow), mirroring the fork, which
// compares the original numeric values and would not have considered such a
// pair equal.
func buildCastFix(pass *analysis.Pass, file *ast.File, call *ast.CallExpr, argExpr ast.Expr, argTV types.TypeAndValue, target types.Type) *ASTFix {
	// Guard constants against value-changing conversions (rules 3 & 10).
	if argTV.Value != nil {
		if tb, ok := target.Underlying().(*types.Basic); ok {
			if !constRepresentable(argTV.Value, tb) {
				return nil
			}
		}
	}

	typeExpr := typeNameExpr(pass.Pkg, file, target)
	if typeExpr == nil {
		return nil
	}

	wrapper := &ast.CallExpr{
		Fun:  typeExpr,
		Args: []ast.Expr{argExpr},
	}
	// Clear synthetic and reused positions so the printer reflows the wrapped
	// expression compactly inline rather than honoring stale offsets.
	clearNodePositions(wrapper)

	return &ASTFix{
		OldNode:  argExpr,
		NewNodes: []ast.Node{wrapper},
	}
}

// typeNameExpr renders target as an ast.Expr spelled the way it appears in the
// scope of file (honoring import aliases), suitable for use as a conversion
// function. Returns nil if the type can't be spelled as a simple conversion
// (e.g. it references a package not imported in this file).
func typeNameExpr(pkg *types.Package, file *ast.File, target types.Type) ast.Expr {
	qual := fileQualifier(pkg, file)
	s := types.TypeString(target, qual)
	// A qualifier that returns "" for an un-imported foreign package would yield
	// a bare type name that doesn't resolve; reject anything we can't spell.
	if s == "" || strings.Contains(s, "invalid type") {
		return nil
	}
	expr, err := parser.ParseExpr(s)
	if err != nil {
		return nil
	}
	clearNodePositions(expr)
	return expr
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
			if imp.Name != nil {
				return imp.Name.Name
			}
			return p.Name()
		}
		// Not imported in this file: fall back to the package name. The target
		// type is always the type of an existing operand, so its package is in
		// practice already imported; this is a defensive default.
		return p.Name()
	}
}

// isForkNumeric reports whether a basic type is numeric in the sense the fork's
// isNumericType used: reflect kinds Int..Complex128. We additionally exclude
// complex here because toFloat64 (the fork's numeric comparison path) does not
// handle complex, and constant casts into complex are out of scope.
func isForkNumeric(b *types.Basic) bool {
	switch b.Kind() {
	case types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
		types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64, types.Uintptr,
		types.Float32, types.Float64:
		return true
	}
	return false
}

// isUntypedLiteral reports whether expr is a bare untyped constant literal
// (e.g. 0, 1.5, 'a', "x"). go/types records the *default* type for such
// literals once they're used as interface{} arguments, so the AST shape is the
// reliable signal here rather than types.IsUntyped.
func isUntypedLiteral(expr ast.Expr) bool {
	_, ok := expr.(*ast.BasicLit)
	return ok
}

// constRepresentable reports whether constant v can be represented exactly in
// basic type b. Used to refuse conversions that would truncate or overflow a
// constant — the fork compared the underlying numeric values, so such a pair
// was never equal and we must not paper over the inequality.
func constRepresentable(v constant.Value, b *types.Basic) bool {
	if v == nil || v.Kind() == constant.Unknown {
		return false
	}
	switch {
	case b.Info()&types.IsInteger != 0:
		iv := constant.ToInt(v)
		if iv.Kind() != constant.Int {
			return false // not an integral value (e.g. 1.5)
		}
		return intInRange(iv, b.Kind())
	case b.Info()&types.IsFloat != 0:
		fv := constant.ToFloat(v)
		if fv.Kind() == constant.Unknown {
			return false
		}
		f, _ := constant.Float64Val(fv)
		return !math.IsInf(f, 0)
	default:
		return false
	}
}

// intInRange reports whether the integer constant v fits in the integer basic
// kind k. types.Int/Uint/Uintptr are treated as 64-bit, matching the analysis
// host and the platforms go-toolchain targets.
func intInRange(v constant.Value, k types.BasicKind) bool {
	switch k {
	case types.Int, types.Int64:
		_, ok := constant.Int64Val(v)
		return ok
	case types.Int8:
		i, ok := constant.Int64Val(v)
		return ok && i >= math.MinInt8 && i <= math.MaxInt8
	case types.Int16:
		i, ok := constant.Int64Val(v)
		return ok && i >= math.MinInt16 && i <= math.MaxInt16
	case types.Int32: // includes rune
		i, ok := constant.Int64Val(v)
		return ok && i >= math.MinInt32 && i <= math.MaxInt32
	case types.Uint, types.Uint64, types.Uintptr:
		if constant.Sign(v) < 0 {
			return false
		}
		_, ok := constant.Uint64Val(v)
		return ok
	case types.Uint8: // includes byte
		u, ok := constant.Uint64Val(v)
		return ok && constant.Sign(v) >= 0 && u <= math.MaxUint8
	case types.Uint16:
		u, ok := constant.Uint64Val(v)
		return ok && constant.Sign(v) >= 0 && u <= math.MaxUint16
	case types.Uint32:
		u, ok := constant.Uint64Val(v)
		return ok && constant.Sign(v) >= 0 && u <= math.MaxUint32
	}
	return false
}

// warnElementMismatch prints a non-fatal warning when an element-comparison
// assertion (Contains, ElementsMatch, Subset, ...) compares a collection whose
// element type differs from the value being searched for. Under the fork these
// could match via loose numeric equality; we don't rewrite them, but we surface
// the case so the behavior change isn't silent.
func warnElementMismatch(pass *analysis.Pass, call *ast.CallExpr, name string, collIdx, valIdx int) {
	collTV, ok1 := pass.TypesInfo.Types[call.Args[collIdx]]
	valTV, ok2 := pass.TypesInfo.Types[call.Args[valIdx]]
	if !ok1 || !ok2 || collTV.Type == nil || valTV.Type == nil {
		return
	}
	elem := elementType(collTV.Type)
	if elem == nil {
		return
	}
	valType := types.Default(valTV.Type)
	if types.Identical(types.Default(elem), valType) {
		return
	}
	eb, ok1 := types.Default(elem).Underlying().(*types.Basic)
	vb, ok2 := valType.Underlying().(*types.Basic)
	if !ok1 || !ok2 || !isForkNumeric(eb) || !isForkNumeric(vb) {
		return
	}
	pos := pass.Fset.Position(call.Pos())
	fmt.Fprintf(os.Stderr,
		"testifycast: warning: %s:%d: %s compares %s elements against %s; the testify fork may have matched these via loose numeric equality, but upstream will not — add an explicit conversion if a match was intended\n",
		pos.Filename, pos.Line, name, elem, valType)
}

// elementType returns the element type of a slice, array, or map value type, or
// nil if the type isn't a collection.
func elementType(t types.Type) types.Type {
	switch u := t.Underlying().(type) {
	case *types.Slice:
		return u.Elem()
	case *types.Array:
		return u.Elem()
	case *types.Map:
		return u.Elem()
	}
	return nil
}

// importsUpstreamTestify reports whether file imports an upstream testify
// package, used as a cheap filter before walking the file.
func importsUpstreamTestify(file *ast.File) bool {
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if path == upstreamAssertPkg || path == upstreamRequirePkg {
			return true
		}
	}
	return false
}
