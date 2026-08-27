package vet

import (
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"sync"

	"golang.org/x/tools/go/analysis"
)

// SliceSetAnalyzer reports a slice the package asks membership of: a literal
// inside slices.Contains, an insert-if-absent append, or a slice whose every
// use is a set operation. Org modules FAIL; others WARN. docs/VET.md
var SliceSetAnalyzer = &analysis.Analyzer{
	Name:       "sliceset",
	Doc:        "detects a slice used as a set; use github.com/wow-look-at-my/go-containers/set instead",
	Run:        runSliceSet,
	ResultType: reflect.TypeOf([]*ASTFixes{}),
}

// sliceSetWarned records file:line of every warning this run, so the package variants that walk one file warn once per site.
var sliceSetWarned sync.Map

// resetSliceSetWarnings forgets prior warnings, so a re-run after a fix reports its sites again.
func resetSliceSetWarnings() { sliceSetWarned.Clear() }

func runSliceSet(pass *analysis.Pass) (any, error) {
	report := pass.Reportf
	if !isOrgModule(pass.Module) {
		report = func(pos token.Pos, format string, args ...any) {
			warnAt(&sliceSetWarned, pass, pos, format, args...)
		}
	}

	candidates := sliceSetCandidates(pass)
	for _, file := range pass.Files {
		reportSliceLiteralLookups(pass, file, report)
		reportInsertIfAbsent(pass, file, report)
		sliceSetUses(pass, file, candidates)
	}

	for _, c := range candidates {
		if c.membership == 0 || c.disqualified {
			continue
		}
		if c.fromLiteral {
			report(c.pos, "a slice the package only asks membership of is a set: use %s.Of(…) instead", setPackage)
			continue
		}
		report(c.pos, "a slice is only ever used as a set: use %s.Set instead", setPackage)
	}

	return []*ASTFixes(nil), nil
}

// reportSliceLiteralLookups reports a membership test against a slice spelled
// on the spot. The literal exists for that one question, so it is a set with
// no name and no other use.
func reportSliceLiteralLookups(pass *analysis.Pass, file *ast.File, report func(token.Pos, string, ...any)) {
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 || !isSlicesLookup(pass, call.Fun) {
			return true
		}
		lit, ok := call.Args[0].(*ast.CompositeLit)
		if !ok || !isSetWorthySlice(pass.TypesInfo.TypeOf(lit)) {
			return true
		}
		report(lit.Pos(), "a slice literal answering one membership question is a set: use %s.Of(…).Contains(…) instead", setPackage)
		return true
	})
}

// reportInsertIfAbsent reports the add-if-not-present shape, whatever else the
// slice does. The guard is a set insert written out, and it costs a scan of
// everything already added.
func reportInsertIfAbsent(pass *analysis.Pass, file *ast.File, report func(token.Pos, string, ...any)) {
	ast.Inspect(file, func(n ast.Node) bool {
		stmt, ok := n.(*ast.IfStmt)
		if !ok || stmt.Else != nil {
			return true
		}
		obj := absenceTest(pass, stmt.Cond)
		if obj == nil || !appendsTo(pass, stmt.Body, obj) {
			return true
		}
		report(stmt.Pos(), "appending only what a scan says is absent is a set insert: use %s.Set instead", setPackage)
		return true
	})
}

// absenceTest reports which slice a condition asks the absence of, or nil.
func absenceTest(pass *analysis.Pass, cond ast.Expr) types.Object {
	switch c := cond.(type) {
	case *ast.UnaryExpr: // !slices.Contains(s, v)
		if c.Op != token.NOT {
			return nil
		}
		return lookupTarget(pass, c.X)
	case *ast.BinaryExpr: // slices.Index(s, v) < 0, == -1
		if c.Op != token.LSS && c.Op != token.EQL {
			return nil
		}
		if !isIntLiteral(c.Y) {
			return nil
		}
		return lookupTarget(pass, c.X)
	}
	return nil
}

// lookupTarget reports the slice a slices.Contains or slices.Index call reads, or nil.
func lookupTarget(pass *analysis.Pass, expr ast.Expr) types.Object {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) == 0 || !isSlicesLookup(pass, call.Fun) {
		return nil
	}
	id, ok := call.Args[0].(*ast.Ident)
	if !ok || !isSetWorthySlice(pass.TypesInfo.TypeOf(id)) {
		return nil
	}
	return pass.TypesInfo.Uses[id]
}

// appendsTo reports whether body appends to obj.
func appendsTo(pass *analysis.Pass, body *ast.BlockStmt, obj types.Object) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isBuiltinCall(pass, call, "append") || len(call.Args) == 0 {
			return true
		}
		if id, ok := call.Args[0].(*ast.Ident); ok && pass.TypesInfo.Uses[id] == obj {
			found = true
		}
		return true
	})
	return found
}

// isSlicesLookup reports whether fun names slices.Contains or slices.Index.
func isSlicesLookup(pass *analysis.Pass, fun ast.Expr) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if sel.Sel.Name != "Contains" && sel.Sel.Name != "Index" {
		return false
	}
	obj := pass.TypesInfo.Uses[sel.Sel]
	if obj == nil || obj.Pkg() == nil {
		id, isIdent := sel.X.(*ast.Ident)
		return isIdent && id.Name == "slices"
	}
	return obj.Pkg().Path() == "slices"
}

// isBuiltinCall reports whether call invokes the named predeclared function.
func isBuiltinCall(pass *analysis.Pass, call *ast.CallExpr, name string) bool {
	id, ok := call.Fun.(*ast.Ident)
	if !ok || id.Name != name {
		return false
	}
	if obj := pass.TypesInfo.ObjectOf(id); obj != nil {
		_, isBuiltin := obj.(*types.Builtin)
		return isBuiltin
	}
	return true
}

// isIntLiteral reports whether expr is an integer constant, with or without a sign.
func isIntLiteral(expr ast.Expr) bool {
	if u, ok := expr.(*ast.UnaryExpr); ok && (u.Op == token.SUB || u.Op == token.ADD) {
		expr = u.X
	}
	lit, ok := expr.(*ast.BasicLit)
	return ok && lit.Kind == token.INT
}

// isSetWorthySlice reports whether t is a slice a set can replace. The element
// must be comparable. A byte slice is a buffer, so it is left alone.
func isSetWorthySlice(t types.Type) bool {
	if t == nil {
		return false
	}
	s, ok := t.Underlying().(*types.Slice)
	if !ok || !types.Comparable(s.Elem()) {
		return false
	}
	b, isBasic := s.Elem().Underlying().(*types.Basic)
	return !isBasic || b.Kind() != types.Byte
}

// sliceSetCandidate is one slice variable and what the package does to it.
type sliceSetCandidate struct {
	pos          token.Pos
	fromLiteral  bool
	membership   int
	disqualified bool
}

// sliceSetCandidates finds every slice variable the package creates itself.
// A parameter, a field or a return value comes from elsewhere, and what to
// store it in is that caller's decision.
func sliceSetCandidates(pass *analysis.Pass) map[types.Object]*sliceSetCandidate {
	candidates := make(map[types.Object]*sliceSetCandidate)
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.AssignStmt:
				if x.Tok != token.DEFINE || len(x.Lhs) != len(x.Rhs) {
					return true
				}
				for i, lhs := range x.Lhs {
					sliceSetAddCandidate(pass, candidates, lhs, x.Rhs[i])
				}
			case *ast.ValueSpec:
				for i, name := range x.Names {
					var value ast.Expr
					if i < len(x.Values) {
						value = x.Values[i]
					}
					sliceSetAddCandidate(pass, candidates, name, value)
				}
			}
			return true
		})
	}
	return candidates
}

// sliceSetAddCandidate records name when it is a slice this package builds:
// a bare var, a literal, or make with length zero. A make with a length holds
// elements the code reaches by index, so it is a buffer.
func sliceSetAddCandidate(pass *analysis.Pass, candidates map[types.Object]*sliceSetCandidate, name, value ast.Expr) {
	id, ok := name.(*ast.Ident)
	if !ok || id.Name == "_" {
		return
	}
	obj := pass.TypesInfo.ObjectOf(id)
	if obj == nil || !isSetWorthySlice(obj.Type()) {
		return
	}
	c := &sliceSetCandidate{pos: id.Pos()}
	switch v := value.(type) {
	case nil: // var s []T
	case *ast.CallExpr:
		if !isBuiltinCall(pass, v, "make") || len(v.Args) < 2 || !isZeroLiteral(v.Args[1]) {
			return
		}
	case *ast.CompositeLit:
		c.fromLiteral = len(v.Elts) > 0
	default:
		return
	}
	candidates[obj] = c
}

// isZeroLiteral reports whether expr is the constant 0.
func isZeroLiteral(expr ast.Expr) bool {
	lit, ok := expr.(*ast.BasicLit)
	return ok && lit.Kind == token.INT && lit.Value == "0"
}

// sliceSetUses classifies every reference to a candidate.
func sliceSetUses(pass *analysis.Pass, file *ast.File, candidates map[types.Object]*sliceSetCandidate) {
	if len(candidates) == 0 {
		return
	}
	var stack []ast.Node
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		if rng, ok := n.(*ast.RangeStmt); ok {
			creditMembershipScan(pass, rng, candidates)
		}
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		c := candidates[pass.TypesInfo.Uses[id]]
		if c == nil {
			return true
		}
		var parent, grandparent ast.Node
		if len(stack) >= 2 {
			parent = stack[len(stack)-2]
		}
		if len(stack) >= 3 {
			grandparent = stack[len(stack)-3]
		}
		classifySliceSetUse(pass, c, id, parent, grandparent)
		return true
	})
}

// creditMembershipScan credits a loop that walks a candidate to compare each
// element against one value. That loop IS slices.Contains, so writing it out
// by hand never escapes this check.
func creditMembershipScan(pass *analysis.Pass, rng *ast.RangeStmt, candidates map[types.Object]*sliceSetCandidate) {
	id, ok := rng.X.(*ast.Ident)
	if !ok {
		return
	}
	c := candidates[pass.TypesInfo.Uses[id]]
	if c == nil || rng.Value == nil {
		return
	}
	if rng.Key != nil && !isBlankIdent(rng.Key) {
		return
	}
	value, ok := rng.Value.(*ast.Ident)
	if !ok || len(rng.Body.List) != 1 {
		return
	}
	stmt, ok := rng.Body.List[0].(*ast.IfStmt)
	if !ok || stmt.Init != nil {
		return
	}
	if comparesToRangeValue(stmt.Cond, value.Name) {
		c.membership++
	}
}

// comparesToRangeValue reports whether cond tests the range value for equality.
func comparesToRangeValue(cond ast.Expr, name string) bool {
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok || bin.Op != token.EQL {
		return false
	}
	for _, side := range []ast.Expr{bin.X, bin.Y} {
		if id, ok := side.(*ast.Ident); ok && id.Name == name {
			return true
		}
	}
	return false
}

// classifySliceSetUse records one use against the candidate. A use that reads
// position or repetition, or that hands the slice to code this walk cannot
// see, disqualifies it.
func classifySliceSetUse(pass *analysis.Pass, c *sliceSetCandidate, id *ast.Ident, parent, grandparent ast.Node) {
	switch p := parent.(type) {
	case *ast.CallExpr:
		classifySliceSetCall(pass, c, id, p, grandparent)
	case *ast.RangeStmt:
		if p.X != id || (p.Key != nil && !isBlankIdent(p.Key)) {
			c.disqualified = true
		}
	case *ast.AssignStmt:
		classifySliceSetAssign(pass, c, id, p)
	case *ast.BinaryExpr:
		// A slice compares against nothing but nil, so this reads emptiness.
		if !isNilIdent(pass, p.X) && !isNilIdent(pass, p.Y) {
			c.disqualified = true
		}
	default:
		c.disqualified = true
	}
}

// classifySliceSetCall records the slice as an argument. len leaves it alone,
// append adds to it, and slices.Contains or a compared slices.Index asks
// membership. Every other call can read position or repetition.
func classifySliceSetCall(pass *analysis.Pass, c *sliceSetCandidate, id *ast.Ident, call *ast.CallExpr, grandparent ast.Node) {
	switch {
	case isBuiltinCall(pass, call, "len"):
	case isBuiltinCall(pass, call, "append"):
		// The slice spread into somebody else's append carries its order along.
		if len(call.Args) == 0 || call.Args[0] != ast.Expr(id) {
			c.disqualified = true
		}
	case isSlicesLookup(pass, call.Fun) && len(call.Args) > 0 && call.Args[0] == ast.Expr(id):
		sel, _ := call.Fun.(*ast.SelectorExpr)
		if sel.Sel.Name == "Index" && !comparedToConstant(grandparent) {
			c.disqualified = true // the position itself is the answer
			return
		}
		c.membership++
	default:
		c.disqualified = true
	}
}

// comparedToConstant reports whether node compares against an integer constant.
func comparedToConstant(node ast.Node) bool {
	bin, ok := node.(*ast.BinaryExpr)
	return ok && (isIntLiteral(bin.X) || isIntLiteral(bin.Y))
}

// classifySliceSetAssign records an assignment naming the slice. Writing the
// whole variable is a reset or an append back; writing one position is not.
func classifySliceSetAssign(pass *analysis.Pass, c *sliceSetCandidate, id *ast.Ident, assign *ast.AssignStmt) {
	for _, lhs := range assign.Lhs {
		if lhs == ast.Expr(id) {
			if assign.Tok != token.ASSIGN && assign.Tok != token.DEFINE {
				c.disqualified = true
			}
			return
		}
	}
	// On the right-hand side the slice is a value copied somewhere else.
	c.disqualified = true
}

// isNilIdent reports whether expr is the predeclared nil.
func isNilIdent(pass *analysis.Pass, expr ast.Expr) bool {
	id, ok := expr.(*ast.Ident)
	if !ok || id.Name != "nil" {
		return false
	}
	if obj := pass.TypesInfo.ObjectOf(id); obj != nil {
		return obj == types.Universe.Lookup("nil")
	}
	return true
}
