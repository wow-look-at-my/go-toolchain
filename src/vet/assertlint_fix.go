package vet

import (
	"go/ast"
	"go/token"
	"go/types"

	"github.com/wow-look-at-my/go-containers/set"
	"golang.org/x/tools/go/analysis"
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

// generateASTFix creates an ASTFix for the if statement. hoisted carries the
// names earlier fixes in the same file have already flattened into each scope.
func generateASTFix(pass *analysis.Pass, ifStmt *ast.IfStmt, assertPkg, assertFunc string, hoisted hoistedNames) *ASTFix {
	// Skip if/else chains (else-if is already filtered during detection)
	if ifStmt.Else != nil {
		return nil
	}

	tVar := getTestVarName(ifStmt.Body)
	if tVar == "" {
		return nil
	}

	// Build the assertion call AST
	assertCall := buildAssertCall(pass, ifStmt.Cond, tVar, assertPkg, assertFunc)
	if assertCall == nil {
		return nil
	}
	assertStmt := &ast.ExprStmt{X: assertCall}

	// Handle init clause case: if x := expr; cond { t.Error } → x := expr; assert.X(...)
	if ifStmt.Init != nil {
		// Special case: if err := X; err != nil → require.NoError(t, X)
		if assertFunc == "NoError" {
			if assign, ok := ifStmt.Init.(*ast.AssignStmt); ok && assign.Tok == token.DEFINE {
				if len(assign.Rhs) == 1 {
					noErrorCall := makeCall(
						makeSelector(assertPkg, "NoError"),
						ast.NewIdent(tVar),
						assign.Rhs[0],
					)
					newNodes := []ast.Node{&ast.ExprStmt{X: noErrorCall}}
					prepareFixNodes(newNodes, ifStmt.Pos())
					return &ASTFix{
						OldNode:  ifStmt,
						NewNodes: newNodes,
					}
				}
			}
		}

		// General case: extract init statement
		newNodes := hoistInit(pass, ifStmt, assertStmt, hoisted)
		prepareFixNodes(newNodes, ifStmt.Pos())
		return &ASTFix{
			OldNode:  ifStmt,
			NewNodes: newNodes,
		}
	}

	// Simple case: if cond { t.Error } → assert.X(t, ...)
	newNodes := []ast.Node{assertStmt}
	prepareFixNodes(newNodes, ifStmt.Pos())
	return &ASTFix{
		OldNode:  ifStmt,
		NewNodes: newNodes,
	}
}

// hoistedNames records, per scope, the names and types that fixes in the same
// run have flattened into that scope. The type checker's scopes describe the
// source as written, where each if's init is private to its if; once an init
// is flattened into the enclosing block its names are visible to every later
// statement there, so the next flattened init resolves against this as well.
type hoistedNames map[*types.Scope]map[string]types.Type

// declared returns the type the name has in scope after the fixes so far: the
// variable the checker sees from pos, or one flattened in by an earlier fix.
// found is false for a name that nothing in scope declares.
func (h hoistedNames) declared(scope *types.Scope, name string, pos token.Pos) (typ types.Type, isVar, found bool) {
	if typ, ok := h[scope][name]; ok {
		return typ, true, true
	}
	_, obj := scope.LookupParent(name, pos)
	if obj == nil {
		return nil, false, false
	}
	v, ok := obj.(*types.Var)
	if !ok {
		return nil, false, true
	}
	return v.Type(), true, true
}

func (h hoistedNames) record(scope *types.Scope, name string, typ types.Type) {
	if h[scope] == nil {
		h[scope] = make(map[string]types.Type)
	}
	h[scope][name] = typ
}

// hoistInit returns the if's init clause and the assertion in a form that is
// legal where the if stood.
//
// An init clause declares into the if's own scope, so `if _, err := f();` is
// legal beside an existing err (it shadows it), beside a later `err :=` in the
// same block, and beside an err of another type. Flattened into the enclosing
// block, each of those is a compile error: "no new variables on left side of
// :=", a redeclaration, or a mismatched assignment. The statements are emitted
// flat when that is provably legal, else kept together in a block, which
// scopes the init exactly as the if did:
//
//   - every name is new in the enclosing scope: `:=` stays as written;
//   - every name exists as a variable of the identical type: `:=` becomes `=`,
//     so the outer variable is written rather than shadowed, which flattening
//     requires anyway since the assertion below must see the value;
//   - a mix of new and existing names, each existing one a variable of the
//     identical type: `:=` stays, declaring the new names and assigning or
//     shadowing the rest;
//   - anything else, or no type information: a block.
func hoistInit(pass *analysis.Pass, ifStmt *ast.IfStmt, assertStmt ast.Stmt, hoisted hoistedNames) []ast.Node {
	block := []ast.Node{&ast.BlockStmt{List: []ast.Stmt{ifStmt.Init, assertStmt}}}
	assign, ok := ifStmt.Init.(*ast.AssignStmt)
	if !ok || assign.Tok != token.DEFINE {
		return []ast.Node{ifStmt.Init, assertStmt} // an assignment or a call declares nothing
	}
	// Scopes[ifStmt] is the scope the init declares into; its parent is where the statement lands.
	ifScope := pass.TypesInfo.Scopes[ifStmt]
	if ifScope == nil || ifScope.Parent() == nil {
		return block
	}
	landing := ifScope.Parent()

	type named struct {
		name string
		typ  types.Type
	}
	var names []named
	anyNew := false
	for _, lhs := range assign.Lhs {
		ident, ok := lhs.(*ast.Ident)
		if !ok {
			return block // not a plain name list
		}
		if ident.Name == "_" {
			continue
		}
		def := pass.TypesInfo.Defs[ident]
		if def == nil {
			return block
		}
		names = append(names, named{ident.Name, def.Type()})
		// A later `name :=` in the landing block would redeclare a name flattened before it.
		if later := landing.Lookup(ident.Name); later != nil && later.Pos() > ifStmt.Pos() {
			return block
		}
		existing, isVar, found := hoisted.declared(landing, ident.Name, ifStmt.Pos())
		if !found {
			anyNew = true
			continue
		}
		if !isVar || !types.Identical(existing, def.Type()) {
			return block
		}
	}
	for _, n := range names {
		hoisted.record(landing, n.name, n.typ)
	}
	if anyNew {
		return []ast.Node{assign, assertStmt}
	}
	flat := *assign
	flat.Tok = token.ASSIGN
	return []ast.Node{&flat, assertStmt}
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
	// Handle negation
	actualCond := cond
	if unary, ok := cond.(*ast.UnaryExpr); ok && unary.Op == token.NOT {
		actualCond = unary.X
	}

	switch c := actualCond.(type) {
	case *ast.CallExpr:
		return buildCallAssert(pass, c, tVar, assertPkg, assertFunc)

	case *ast.BinaryExpr:
		return buildBinaryAssert(pass, c, tVar, assertPkg, assertFunc)

	case *ast.Ident:
		// assert.True(t, x) or assert.False(t, x)
		return makeCall(makeSelector(assertPkg, assertFunc), ast.NewIdent(tVar), c)
	}

	// Fallback: use actualCond (with negation unwrapped)
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

	// Fallback: wrap the entire call
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

	// Default: the plain argument pair
	return makeCall(makeSelector(assertPkg, assertFunc), ast.NewIdent(tVar), bin.X, bin.Y)
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
