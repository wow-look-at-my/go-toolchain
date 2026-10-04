package vet

import (
	"go/ast"
	"go/token"
	"go/types"

	"github.com/wow-look-at-my/go-containers/set"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/astutil"
)

// getTestVarName extracts the test variable name (t or b) from the body.
func getTestVarName(body *ast.BlockStmt) string {
	for _, stmt := range body.List {
		if exprStmt, ok := stmt.(*ast.ExprStmt); ok {
			if call, ok := exprStmt.X.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					if ident, ok := sel.X.(*ast.Ident); ok {
						return ident.Name
					}
				}
			}
		}
	}
	return ""
}

// generateASTFix creates an ASTFix for the if statement. report is false when
// the if is not one the fixer can rewrite without changing what the test
// does or breaking the build; such an if is left alone and not reported.
// A nil fix with report true is a finding with no automatic rewrite.
func generateASTFix(pass *analysis.Pass, st *assertFixState, ifStmt *ast.IfStmt, assertPkg, assertFunc string) (fix *ASTFix, report bool) {
	// Skip if/else chains (else-if is already filtered during detection)
	if ifStmt.Else != nil {
		return nil, true
	}

	tVar := getTestVarName(ifStmt.Body)
	if tVar == "" {
		return nil, true
	}

	// An if inside one this pass already replaces is rewritten on the re-run, against the new tree.
	if st.insideReplaced(ifStmt) {
		return nil, false
	}

	errCall, rest, ok := splitFailureBody(ifStmt.Body, assertPkg)
	if !ok {
		return nil, false
	}
	dropped := []ast.Node{errCall}
	if assertPkg == "require" {
		// Statements after t.Fatal never run; they go with it.
		for _, s := range rest {
			dropped = append(dropped, s)
		}
		rest = nil
	}
	if st.dropOrphansVar(ifStmt, dropped) {
		return nil, false
	}

	var commit func()
	var newNodes []ast.Node
	switch {
	case isNoErrorInit(ifStmt, assertFunc):
		// Special case: if err := X; err != nil → require.NoError(t, X)
		assign := ifStmt.Init.(*ast.AssignStmt)
		noErrorCall := makeCall(
			makeSelector(assertPkg, "NoError"),
			ast.NewIdent(tVar),
			assign.Rhs[0],
		)
		newNodes = []ast.Node{assertionStmt(noErrorCall, rest)}

	case ifStmt.Init == nil:
		// Simple case: if cond { t.Error } → assert.X(t, ...)
		assertCall := buildAssertCall(pass, ifStmt.Cond, tVar, assertPkg, assertFunc)
		if assertCall == nil {
			return nil, false
		}
		newNodes = []ast.Node{assertionStmt(assertCall, rest)}

	default:
		// Init clause: if x := expr; cond { t.Error } → x := expr; assert.X(...)
		assertCall := buildAssertCall(pass, ifStmt.Cond, tVar, assertPkg, assertFunc)
		if assertCall == nil {
			return nil, false
		}
		var init ast.Stmt
		init, commit, ok = st.hoistInit(ifStmt)
		if !ok {
			return nil, false
		}
		newNodes = []ast.Node{init, assertionStmt(assertCall, rest)}
	}

	if commit != nil {
		commit()
	}
	st.replaced = append(st.replaced, ifStmt)
	prepareFixNodes(newNodes, ifStmt.Pos())
	return &ASTFix{
		OldNode:  ifStmt,
		NewNodes: newNodes,
	}, true
}

// isNoErrorInit reports the `if err := X; err != nil` shape
// determineAssertion names NoError, with the right-hand side the rewrite
// passes on.
func isNoErrorInit(ifStmt *ast.IfStmt, assertFunc string) bool {
	if assertFunc != "NoError" {
		return false
	}
	assign, ok := ifStmt.Init.(*ast.AssignStmt)
	return ok && assign.Tok == token.DEFINE && len(assign.Rhs) == 1
}

// assertionStmt is the statement an assertion call becomes. Statements that
// followed t.Error in the if body ran only when the check failed, so they stay
// behind the assertion's result: `if !assert.X(...) { continue }`.
func assertionStmt(call *ast.CallExpr, rest []ast.Stmt) ast.Stmt {
	if len(rest) == 0 {
		return &ast.ExprStmt{X: call}
	}
	return &ast.IfStmt{
		Cond: &ast.UnaryExpr{Op: token.NOT, X: call},
		Body: &ast.BlockStmt{List: rest},
	}
}

// splitFailureBody splits an if body into its leading t.Error/t.Fatal call
// and the statements after it. A body that does anything before the failure
// call is not a plain assertion: the rewrite would reorder or drop that work.
// A t.Error-led body under a require (a t.Fatal further down) is refused too:
// require would stop the test before the statements between them run.
func splitFailureBody(body *ast.BlockStmt, assertPkg string) (*ast.CallExpr, []ast.Stmt, bool) {
	if body == nil || len(body.List) == 0 {
		return nil, nil, false
	}
	exprStmt, ok := body.List[0].(*ast.ExprStmt)
	if !ok {
		return nil, nil, false
	}
	call, ok := exprStmt.X.(*ast.CallExpr)
	if !ok || !isTestingErrorCall(call) {
		return nil, nil, false
	}
	fatal := isFatalCall(call)
	if fatal != (assertPkg == "require") {
		return nil, nil, false
	}
	return call, body.List[1:], true
}

// isFatalCall reports t.Fatal/t.Fatalf.
func isFatalCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && (sel.Sel.Name == "Fatal" || sel.Sel.Name == "Fatalf")
}

// assertFixState is what one file's assertlint pass knows across its fixes.
// Every fix is computed against the same type-checked tree, so a hoist has to
// see the names the earlier hoists in its block already declared.
type assertFixState struct {
	pass *analysis.Pass
	file *ast.File

	// hoisted maps a block scope to the names fixes declared into it and their types.
	hoisted map[*types.Scope]map[string]types.Type
	// replaced are the ifs this pass rewrites.
	replaced []ast.Node

	// uses lists every identifier that refers to an object, in source order.
	uses map[types.Object][]*ast.Ident
	// writes are identifiers standing alone as an assignment or ++/-- target.
	writes set.Set[*ast.Ident]
	// assigns maps each write identifier to its assignment statement.
	assigns map[*ast.Ident]*ast.AssignStmt
	// unusedExempt are declarations the compiler never reports unused: parameters and results, plus range variables.
	unusedExempt set.Set[token.Pos]
}

func newAssertFixState(pass *analysis.Pass, file *ast.File) *assertFixState {
	st := &assertFixState{
		pass:         pass,
		file:         file,
		hoisted:      make(map[*types.Scope]map[string]types.Type),
		uses:         make(map[types.Object][]*ast.Ident),
		writes:       set.New[*ast.Ident](),
		assigns:      make(map[*ast.Ident]*ast.AssignStmt),
		unusedExempt: set.New[token.Pos](),
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if obj := pass.TypesInfo.Uses[x]; obj != nil {
				st.uses[obj] = append(st.uses[obj], x)
			}
		case *ast.AssignStmt:
			for _, lhs := range x.Lhs {
				if id, ok := lhs.(*ast.Ident); ok {
					st.writes.Add(id)
					st.assigns[id] = x
				}
			}
		case *ast.IncDecStmt:
			if id, ok := x.X.(*ast.Ident); ok {
				st.writes.Add(id)
			}
		case *ast.RangeStmt:
			for _, e := range []ast.Expr{x.Key, x.Value} {
				if id, ok := e.(*ast.Ident); ok {
					st.unusedExempt.Add(id.Pos())
				}
			}
		case *ast.FuncType:
			for _, fl := range []*ast.FieldList{x.Params, x.Results} {
				if fl == nil {
					continue
				}
				for _, f := range fl.List {
					for _, id := range f.Names {
						st.unusedExempt.Add(id.Pos())
					}
				}
			}
		}
		return true
	})
	return st
}

// insideReplaced reports whether n sits inside an if this pass already rewrites.
func (st *assertFixState) insideReplaced(n ast.Node) bool {
	for _, r := range st.replaced {
		if n.Pos() >= r.Pos() && n.End() <= r.End() {
			return true
		}
	}
	return false
}

// dropOrphansVar reports whether deleting the dropped nodes leaves a local
// variable declared and not used: one the failure message was the only
// reader of. The compiler rejects that, so such an if is not rewritten.
func (st *assertFixState) dropOrphansVar(ifStmt *ast.IfStmt, dropped []ast.Node) bool {
	inDropped := func(pos token.Pos) bool {
		for _, d := range dropped {
			if pos >= d.Pos() && pos < d.End() {
				return true
			}
		}
		return false
	}
	orphaned := false
	for _, d := range dropped {
		ast.Inspect(d, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok || orphaned {
				return !orphaned
			}
			v, ok := st.pass.TypesInfo.Uses[id].(*types.Var)
			if !ok || v.IsField() || v.Parent() == nil || v.Parent() == st.pass.Pkg.Scope() {
				return true
			}
			// A name the if itself declares leaves along with it (the NoError shape).
			if v.Pos() >= ifStmt.Pos() && v.Pos() < ifStmt.End() {
				return true
			}
			if st.unusedExempt.Contains(v.Pos()) {
				return true
			}
			for _, u := range st.uses[v] {
				if !st.writes.Contains(u) && !inDropped(u.Pos()) {
					return true
				}
			}
			orphaned = true
			return false
		})
	}
	return orphaned
}

// hoistInit returns the if's init clause in a form legal outside the if, and
// a commit that records what it declared once the fix is kept. ok is false
// when no spelling of the init is both legal in the enclosing block and keeps
// every other statement there reading the variable it read before.
//
// An init clause declares into the if's own scope. Lifted into the enclosing
// block, each name it declares must be one of.
//   - new there and not shadowing anything a later statement in the block
//     reads, so := declares it;
//   - a variable the block already declared earlier, of the same type, that
//     nothing reads before the block next overwrites it, so it is reused;
//   - a name an earlier hoist in this pass declared there with the same type.
func (st *assertFixState) hoistInit(ifStmt *ast.IfStmt) (ast.Stmt, func(), bool) {
	parentList, ok := st.enclosingStmtList(ifStmt)
	if !ok {
		return nil, nil, false // not in a statement list, so there is nowhere to put a second statement.
	}
	assign, ok := ifStmt.Init.(*ast.AssignStmt)
	if !ok || assign.Tok != token.DEFINE {
		return ifStmt.Init, nil, true
	}
	// Scopes[ifStmt] is the scope the init declares into; its parent is where the statement lands.
	ifScope := st.pass.TypesInfo.Scopes[ifStmt]
	if ifScope == nil || ifScope.Parent() == nil {
		return nil, nil, false
	}
	block := ifScope.Parent()
	prior := st.hoisted[block]

	defined := make(map[string]types.Type)
	for _, lhs := range assign.Lhs {
		ident, ok := lhs.(*ast.Ident)
		if !ok {
			return nil, nil, false
		}
		if ident.Name == "_" {
			continue
		}
		def := st.pass.TypesInfo.Defs[ident]
		if def == nil {
			return nil, nil, false
		}
		typ := def.Type()

		if existing := block.Lookup(ident.Name); existing != nil {
			if existing.Pos() > ifStmt.Pos() {
				return nil, nil, false // declared later in the block
			}
			if !types.Identical(existing.Type(), typ) || !st.overwriteIsDead(existing, ifStmt, parentList) {
				return nil, nil, false
			}
			continue
		}
		if prevType, ok := prior[ident.Name]; ok {
			if !types.Identical(prevType, typ) {
				return nil, nil, false
			}
			continue
		}
		if _, outer := block.LookupParent(ident.Name, ifStmt.Pos()); outer != nil && st.usedWithin(outer, ifStmt.End(), block.End()) {
			return nil, nil, false // the rest of the block reads the outer name
		}
		defined[ident.Name] = typ
	}

	hoisted := *assign
	if len(defined) == 0 {
		hoisted.Tok = token.ASSIGN // every name exists already, and := needs one new name.
	}
	commit := func() {
		if st.hoisted[block] == nil {
			st.hoisted[block] = make(map[string]types.Type)
		}
		for name, typ := range defined {
			st.hoisted[block][name] = typ
		}
	}
	return &hoisted, commit, true
}

// enclosingStmtList returns the statement list n is a direct element of.
func (st *assertFixState) enclosingStmtList(n ast.Stmt) ([]ast.Stmt, bool) {
	path, _ := astutil.PathEnclosingInterval(st.file, n.Pos(), n.End())
	if len(path) < 2 || path[0] != n {
		return nil, false
	}
	switch p := path[1].(type) {
	case *ast.BlockStmt:
		return p.List, true
	case *ast.CaseClause:
		return p.Body, true
	case *ast.CommClause:
		return p.Body, true
	}
	return nil, false
}

// usedWithin reports whether any identifier in (from, to) refers to obj.
func (st *assertFixState) usedWithin(obj types.Object, from, to token.Pos) bool {
	for _, u := range st.uses[obj] {
		if u.Pos() > from && u.Pos() < to {
			return true
		}
	}
	return false
}

// overwriteIsDead reports whether assigning obj in place of ifStmt's init
// changes nothing else: the first reference to obj after the if is a plain
// assignment in the same statement list, which does not read obj itself.
func (st *assertFixState) overwriteIsDead(obj types.Object, ifStmt *ast.IfStmt, list []ast.Stmt) bool {
	var first *ast.Ident
	for _, u := range st.uses[obj] {
		if u.Pos() > ifStmt.End() && (first == nil || u.Pos() < first.Pos()) {
			first = u
		}
	}
	if first == nil {
		return true
	}
	if !st.writes.Contains(first) {
		return false
	}
	assign := st.assigns[first]
	if assign == nil || (assign.Tok != token.ASSIGN && assign.Tok != token.DEFINE) {
		return false // x += 1 reads x
	}
	inList := false
	for _, s := range list {
		if s == ast.Stmt(assign) {
			inList = true
			break
		}
	}
	if !inList {
		return false // a write inside a branch or loop may not happen
	}
	for _, rhs := range assign.Rhs {
		for _, u := range st.uses[obj] {
			if u.Pos() >= rhs.Pos() && u.Pos() < rhs.End() {
				return false
			}
		}
	}
	return true
}

// makeSelector creates a pkg.method selector expression.
func makeSelector(pkg, method string) *ast.SelectorExpr {
	return &ast.SelectorExpr{
		X:   ast.NewIdent(pkg),
		Sel: ast.NewIdent(method),
	}
}

// makeCall creates a function call with given arguments.
func makeCall(fun ast.Expr, args ...ast.Expr) *ast.CallExpr {
	return &ast.CallExpr{
		Fun:  fun,
		Args: args,
	}
}

// buildAssertCall builds the assertion call AST node.
func buildAssertCall(pass *analysis.Pass, cond ast.Expr, tVar, assertPkg, assertFunc string) *ast.CallExpr {
	// Handle negation. determineAssertFunc sees through parentheses, `!(a > b)` included.
	actualCond := ast.Unparen(cond)
	if unary, ok := actualCond.(*ast.UnaryExpr); ok && unary.Op == token.NOT {
		actualCond = ast.Unparen(unary.X)
	}

	switch c := actualCond.(type) {
	case *ast.CallExpr:
		return buildCallAssert(pass, c, tVar, assertPkg, assertFunc)

	case *ast.BinaryExpr:
		return buildBinaryAssert(pass, c, tVar, assertPkg, assertFunc)
	}

	// Anything else is a boolean value: assert.True(t, x) or assert.False(t, x).
	if assertFunc != "True" && assertFunc != "False" {
		return nil
	}
	return makeCall(makeSelector(assertPkg, assertFunc), ast.NewIdent(tVar), actualCond)
}

// buildCallAssert generates assertion for call expressions.
func buildCallAssert(pass *analysis.Pass, call *ast.CallExpr, tVar, assertPkg, assertFunc string) *ast.CallExpr {
	funcName := getCallFuncName(call)

	switch funcName {
	case "strings.Contains":
		if len(call.Args) == 2 {
			// assert.Contains(t, haystack, needle)
			return makeCall(makeSelector(assertPkg, assertFunc), ast.NewIdent(tVar), call.Args[0], call.Args[1])
		}

	case "strings.HasPrefix", "strings.HasSuffix":
		if len(call.Args) == 2 {
			// assert.True(t, strings.HasPrefix(s, prefix))
			return makeCall(makeSelector(assertPkg, assertFunc), ast.NewIdent(tVar), call)
		}

	case "reflect.DeepEqual":
		if len(call.Args) == 2 {
			// assert.Equal(t, a, b)
			return makeCall(makeSelector(assertPkg, assertFunc), ast.NewIdent(tVar), call.Args[0], call.Args[1])
		}
	}

	// Fallback: the call is the boolean asserted on.
	if assertFunc != "True" && assertFunc != "False" {
		return nil
	}
	return makeCall(makeSelector(assertPkg, assertFunc), ast.NewIdent(tVar), call)
}

// buildBinaryAssert generates assertion for binary expressions.
func buildBinaryAssert(pass *analysis.Pass, bin *ast.BinaryExpr, tVar, assertPkg, assertFunc string) *ast.CallExpr {
	// For compound conditions (&&, ||), wrap the whole expression
	switch bin.Op {
	case token.LAND, token.LOR:
		return makeCall(makeSelector(assertPkg, assertFunc), ast.NewIdent(tVar), bin)
	}

	switch assertFunc {
	case "Equal", "NotEqual":
		// assert.Equal(t, expected, actual)
		expected, actual := bin.Y, bin.X
		// Add type cast if needed for numeric literals
		if lit, isLit := bin.Y.(*ast.BasicLit); isLit {
			typ := pass.TypesInfo.TypeOf(bin.X)
			if typ != nil {
				if castType := castableType(lit, typ.String()); castType != "" {
					expected = &ast.CallExpr{
						Fun:  ast.NewIdent(castType),
						Args: []ast.Expr{bin.Y},
					}
				}
			}
		} else if lit, isLit := bin.X.(*ast.BasicLit); isLit {
			typ := pass.TypesInfo.TypeOf(bin.Y)
			if typ != nil {
				if castType := castableType(lit, typ.String()); castType != "" {
					actual = &ast.CallExpr{
						Fun:  ast.NewIdent(castType),
						Args: []ast.Expr{bin.X},
					}
				}
			}
		}
		return makeCall(makeSelector(assertPkg, assertFunc), ast.NewIdent(tVar), expected, actual)

	case "Nil", "NotNil":
		// assert.Nil(t, value)
		return makeCall(makeSelector(assertPkg, assertFunc), ast.NewIdent(tVar), bin.X)

	case "Less", "Greater", "LessOrEqual", "GreaterOrEqual":
		// assert.Less(t, left, right)
		return makeCall(makeSelector(assertPkg, assertFunc), ast.NewIdent(tVar), bin.X, bin.Y)
	}

	// Default: the comparison is the boolean asserted on.
	if assertFunc != "True" && assertFunc != "False" {
		return nil
	}
	return makeCall(makeSelector(assertPkg, assertFunc), ast.NewIdent(tVar), bin)
}

// clearNodePositions recursively clears position information from all AST nodes
// in the subtree. This prevents the Go printer from interleaving comments based
// on stale position information when AST nodes are reused in a different context
// (e.g., extracting condition operands from an if statement into assert call arguments).
func clearNodePositions(node ast.Node) {
	InspectNode(node, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		switch x := n.(type) {
		case *ast.Ident:
			x.NamePos = token.NoPos
		case *ast.BasicLit:
			x.ValuePos = token.NoPos
		case *ast.BinaryExpr:
			x.OpPos = token.NoPos
		case *ast.UnaryExpr:
			x.OpPos = token.NoPos
		case *ast.ParenExpr:
			x.Lparen = token.NoPos
			x.Rparen = token.NoPos
		case *ast.CallExpr:
			x.Lparen = token.NoPos
			x.Rparen = token.NoPos
			x.Ellipsis = token.NoPos
		case *ast.IndexExpr:
			x.Lbrack = token.NoPos
			x.Rbrack = token.NoPos
		case *ast.StarExpr:
			x.Star = token.NoPos
		case *ast.CompositeLit:
			x.Lbrace = token.NoPos
			x.Rbrace = token.NoPos
		case *ast.KeyValueExpr:
			x.Colon = token.NoPos
		case *ast.SliceExpr:
			x.Lbrack = token.NoPos
			x.Rbrack = token.NoPos
		case *ast.TypeAssertExpr:
			x.Lparen = token.NoPos
			x.Rparen = token.NoPos
		case *ast.AssignStmt:
			x.TokPos = token.NoPos
		case *ast.BranchStmt:
			x.TokPos = token.NoPos
		case *ast.ReturnStmt:
			x.Return = token.NoPos
		}
		return true
	})
}

// prepareFixNodes clears stale positions from all new nodes and sets the leading
// token position to pos, so the Go printer flushes leading comments correctly.
func prepareFixNodes(nodes []ast.Node, pos token.Pos) {
	for _, node := range nodes {
		clearNodePositions(node)
	}
	if len(nodes) > 0 {
		setFirstTokenPos(nodes[0], pos)
	}
}

// setFirstTokenPos walks the AST depth-wise and sets the position of the leading
// positioned token (Ident or BasicLit) to pos.
func setFirstTokenPos(node ast.Node, pos token.Pos) {
	done := false
	InspectNode(node, func(n ast.Node) bool {
		if done || n == nil {
			return false
		}
		switch x := n.(type) {
		case *ast.Ident:
			x.NamePos = pos
			done = true
			return false
		case *ast.BasicLit:
			x.ValuePos = pos
			done = true
			return false
		}
		return true
	})
}

// castableType returns the type to cast the literal to, or empty string if no cast needed.
// Only returns basic types that don't require imports.
func castableType(lit *ast.BasicLit, targetType string) string {
	// Only cast to simple builtin types (no package imports needed)
	basicTypes := set.Of(
		"int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64",
		"byte", "rune",
	)

	if !basicTypes.Contains(targetType) {
		return "" // Skip complex types that would require imports
	}

	switch lit.Kind {
	case token.INT:
		// Integer literals default to int, need cast for other integer types
		if targetType != "int" {
			return targetType
		}
	case token.FLOAT:
		// Float literals default to float64, need cast for float32
		if targetType != "float64" {
			return targetType
		}
	}
	return ""
}
