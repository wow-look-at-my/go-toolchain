package vet

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"math"
	"os"
	"reflect"
	"sort"
	"strings"

	ansi "github.com/wow-look-at-my/ansi-writer"
	"golang.org/x/tools/go/analysis"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// Upstream testify package paths. The toolchain rewrites the in-house fork
// (github.com/wow-look-at-my/testify) back to these before analysis runs, so by
// the time this analyzer executes the calls resolve to upstream.
const (
	upstreamAssertPkg  = "github.com/stretchr/testify/assert"
	upstreamRequirePkg = "github.com/stretchr/testify/require"
)

// TestifyCastAnalyzer inserts explicit type conversions into the arguments of
// upstream testify two-operand comparison assertions (assert/require
// .Equal/.NotEqual and the ordering family .Greater/.GreaterOrEqual/.Less/
// .LessOrEqual, plus their f-variants, in both package and *Assertions method
// form).
//
// The wow-look-at-my/testify fork loosened its comparisons so that
// numerically-equal (or ordered) values of different convertible types
// compared fine (e.g. assert.Equal(t, 0, f) with f float64). Upstream testify
// is type-strict on both paths: Equal/NotEqual use reflect.DeepEqual, and the
// ordering assertions go through compareTwoValues, which fails with "Elements
// should be the same type" when the operand kinds differ (e.g.
// assert.Greater(t, v, 0) with v int16). To keep code that relied on the fork
// compiling and passing against upstream, this analyzer makes the two
// compared operands the same static type by wrapping one of them in a
// conversion — mirroring exactly the cases the fork would have treated as
// equal (or comparable).
//
// Fixes are emitted as surgical byte edits (CastEdits) rather than whole-file
// AST reprints, so all surrounding formatting and comments are preserved.
var TestifyCastAnalyzer = &analysis.Analyzer{
	Name:       "testifycast",
	Doc:        "inserts type conversions for cross-type testify Equal/NotEqual and Greater/Less-family assertions so they pass against upstream testify",
	Run:        runTestifyCast,
	ResultType: reflect.TypeOf([]*CastEdits{}),
}

// CastEdit records a single conversion to insert: wrap the source span
// [Start,End) in TypeName(...).
type CastEdit struct {
	Start, End token.Pos
	TypeName   string
}

// CastEdits is the set of conversions to apply to one file.
type CastEdits struct {
	Filename string
	Fset     *token.FileSet
	Edits    []CastEdit
}

// equalityFuncs are the testify assertions whose semantics changed in the fork
// because they route argument comparison through ObjectsAreEqual. These are the
// ones we rewrite with a conversion.
var equalityFuncs = map[string]bool{
	"Equal": true, "Equalf": true,
	"NotEqual": true, "NotEqualf": true,
}

// orderingFuncs are the testify ordering assertions. Upstream routes them
// through compareTwoValues, which requires the two operands to have the same
// reflect kind and fails with "Elements should be the same type" otherwise —
// so e.g. assert.Greater(t, v, 0) with v int16 fails upstream (0 is an int)
// even though the fork's loose numeric comparison accepted it. They take the
// same (expected, actual) operand shape as Equal and get the same conversion
// treatment.
var orderingFuncs = map[string]bool{
	"Greater": true, "Greaterf": true,
	"GreaterOrEqual": true, "GreaterOrEqualf": true,
	"Less": true, "Lessf": true,
	"LessOrEqual": true, "LessOrEqualf": true,
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
			case equalityFuncs[name] || orderingFuncs[name]:
				if edit := castEditForEqual(pass, file, call, expIdx, actIdx); edit != nil {
					ce := fileToEdits[file]
					if ce == nil {
						ce = &CastEdits{Filename: filename, Fset: pass.Fset}
						fileToEdits[file] = ce
					}
					ce.Edits = append(ce.Edits, *edit)
				}
			case elementFuncs[name]:
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

// castEditForEqual decides whether a conversion is needed to make the two
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

	expNumeric := isForkNumeric(expBasic)
	actNumeric := isForkNumeric(actBasic)

	switch {
	case expNumeric && actNumeric:
		// Rule 3: numeric mismatch. Pick which side to convert. If the actual
		// is an untyped literal and the expected is not, convert the actual
		// (the literal) to the expected's type; otherwise convert the expected
		// to the actual's type (keeps the value-under-test visible).
		if isUntypedLiteral(actExpr) && !isUntypedLiteral(expExpr) {
			return buildCastEdit(pass, file, actExpr, actTV, expType)
		}
		return buildCastEdit(pass, file, expExpr, expTV, actType)

	case expNumeric != actNumeric:
		// One numeric, one not: the fork required matching kinds, so this pair
		// compared false. No sound cast (rule 2).
		return nil

	default:
		// Rule 5: neither numeric. The fork converts only when Kind() matches
		// and the types are convertible, then DeepEqual. Mirror that for
		// same-kind convertible named types (e.g. type Name string vs string).
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
	// Guard numeric constants against value-changing conversions (truncation /
	// overflow). Non-numeric conversions (e.g. string -> named string type) are
	// always representable, so the guard only applies to numeric targets.
	if argTV.Value != nil {
		if tb, ok := target.Underlying().(*types.Basic); ok && tb.Info()&types.IsNumeric != 0 {
			if !constRepresentable(argTV.Value, tb) {
				return nil
			}
		}
	}

	name := types.TypeString(target, fileQualifier(pass.Pkg, file))
	if name == "" || strings.ContainsAny(name, " \t\n") || strings.Contains(name, "invalid") {
		// Anything we can't spell as a bare conversion function: skip rather
		// than emit something that won't compile.
		return nil
	}

	return &CastEdit{
		Start:    argExpr.Pos(),
		End:      argExpr.End(),
		TypeName: name,
	}
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
					// Dot import: the package's identifiers are in file scope, so
					// the type is spelled unqualified (Duration, not .Duration).
					return ""
				}
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
		if math.IsInf(f, 0) {
			return false // overflows float64
		}
		if b.Kind() == types.Float32 && math.IsInf(float64(float32(f)), 0) {
			return false // value is finite in float64 but overflows float32
		}
		return true
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
	// For collection-vs-collection assertions (ElementsMatch/Subset/NotSubset)
	// the second operand is itself a collection, so compare its element type;
	// for value-in-collection (Contains) the second operand is a scalar value.
	cmpType := types.Default(valTV.Type)
	if e2 := elementType(valTV.Type); e2 != nil {
		cmpType = types.Default(e2)
	}
	if types.Identical(types.Default(elem), cmpType) {
		return
	}
	eb, ok1 := types.Default(elem).Underlying().(*types.Basic)
	vb, ok2 := cmpType.Underlying().(*types.Basic)
	if !ok1 || !ok2 || !isForkNumeric(eb) || !isForkNumeric(vb) {
		return
	}
	pos := pass.Fset.Position(call.Pos())
	logger.Warn(
		"testifycast: warning: %s:%d: %s compares %s elements against %s; the testify fork may have matched these via loose numeric equality, but upstream will not — add an explicit conversion if a match was intended",
		pos.Filename, pos.Line, name, elem, cmpType)
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

// rendered returns the file's source with all edits applied, without writing to
// disk. Edits are applied right-to-left so byte offsets stay valid.
func (c *CastEdits) rendered(src []byte) []byte {
	edits := append([]CastEdit(nil), c.Edits...)
	sort.Slice(edits, func(i, j int) bool { return edits[i].Start > edits[j].Start })

	out := src
	for _, e := range edits {
		s := c.Fset.Position(e.Start).Offset
		en := c.Fset.Position(e.End).Offset
		if s < 0 || en > len(out) || s > en {
			continue
		}
		var b []byte
		b = append(b, out[:s]...)
		b = append(b, e.TypeName...)
		b = append(b, '(')
		b = append(b, out[s:en]...)
		b = append(b, ')')
		b = append(b, out[en:]...)
		out = b
	}
	return out
}

// Apply routes the file with all edits applied through ed: a fix-mode editor
// rewrites it on disk (and a fix line is printed per edit), a check-mode (CI)
// editor records a violation. testifycast emits no analyzer diagnostic of its
// own, so this recorded violation is what fails CI. Returns whether it wrote.
func (c *CastEdits) Apply(ed Editor) (bool, error) {
	src, err := os.ReadFile(c.Filename)
	if err != nil {
		return false, err
	}
	wrote, err := ed.Require(c.Filename, c.rendered(src), "testify Equal/NotEqual/Greater/Less needs explicit type conversions for upstream testify")
	if err != nil {
		return false, err
	}
	if wrote {
		for _, e := range c.Edits {
			c.printEdit(src, e)
		}
	}
	return wrote, nil
}

// printEdit prints a colored old -> new line for a single conversion.
func (c *CastEdits) printEdit(src []byte, e CastEdit) {
	pos := c.Fset.Position(e.Start)
	loc := SourceLocation{File: pos.Filename, Line: pos.Line, Column: pos.Column}
	s := c.Fset.Position(e.Start).Offset
	en := c.Fset.Position(e.End).Offset
	old := ""
	if s >= 0 && en <= len(src) && s <= en {
		old = string(src[s:en])
	}
	yellow := ansi.Concat(ansi.Yellow.FG, "fixed:", ansi.Reset)
	grey := ansi.Concat(ansi.BrightBlack.FG, loc.ShortLoc(), ansi.Reset)
	red := ansi.Concat(ansi.Red.FG, old, ansi.Reset)
	green := ansi.Concat(ansi.Green.FG, e.TypeName+"("+old+")", ansi.Reset)
	logger.Info("%s %s %s → %s", yellow, grey, red, green)
}
