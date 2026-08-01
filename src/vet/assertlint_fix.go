package vet

import (
	"go/ast"
	"go/token"

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

// generateASTFix creates an ASTFix for the if statement.
func generateASTFix(pass *analysis.Pass, ifStmt *ast.IfStmt, assertPkg, assertFunc string) *ASTFix {
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
		newNodes := []ast.Node{hoistableInit(pass, ifStmt), assertStmt}
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

// hoistableInit returns the if statement's init clause in a form that is legal
// OUTSIDE the if.
//
// An init clause declares into the if's own scope, so `if _, err := f();` is
// legal even where an `err` already exists -- it shadows it. Lift that same
// statement into the enclosing block verbatim and the shadowing is gone: Go
// answers "no new variables on left side of :=" and the package no longer
// compiles. The fixer wrote that into two files of a repo it was asked to
// tidy, and the run died on its OWN output, after printing thirty green
// "fixed:" lines.
//
// So when every name being defined already exists in an enclosing scope, the
// hoisted statement assigns instead of defining. When ANY name is new, `:=`
// stays correct (Go needs just one new variable on the left) and the statement
// is returned untouched.
//
// The conversion means the outer variable is now written rather than shadowed.
// That is inherent to flattening the if -- the assertion below it has to see
// the value -- and it is what a person writes by hand when they make this same
// edit.
func hoistableInit(pass *analysis.Pass, ifStmt *ast.IfStmt) ast.Stmt {
	assign, ok := ifStmt.Init.(*ast.AssignStmt)
	if !ok || assign.Tok != token.DEFINE {
		return ifStmt.Init
	}
	// Scopes[ifStmt] is the scope the init declares into; its parent is where
	// the statement is about to land.
	ifScope := pass.TypesInfo.Scopes[ifStmt]
	if ifScope == nil || ifScope.Parent() == nil {
		return ifStmt.Init // no type info: leave it exactly as it was
	}
	for _, lhs := range assign.Lhs {
		ident, ok := lhs.(*ast.Ident)
		if !ok {
			return ifStmt.Init // not a plain name list; do not touch it
		}
		if ident.Name == "_" {
			continue
		}
		if _, obj := ifScope.Parent().LookupParent(ident.Name, ifStmt.Pos()); obj == nil {
			return ifStmt.Init // at least one new name, so := is legal
		}
	}
	hoisted := *assign
	hoisted.Tok = token.ASSIGN
	return &hoisted
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

	// Default: two args
	return makeCall(makeSelector(assertPkg, assertFunc), ast.NewIdent(tVar), bin.X, bin.Y)
}

// clearNodePositions recursively clears position information from all AST nodes
// in the subtree. This prevents the Go printer from interleaving comments based
// on stale position information when AST nodes are reused in a different context
// (e.g., extracting condition operands from an if statement into assert call arguments).
func clearNodePositions(node ast.Node) {
	ast.Inspect(node, func(n ast.Node) bool {
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

// prepareFixNodes clears stale positions from all new nodes and sets the first
// token position to pos, so the Go printer flushes leading comments correctly.
func prepareFixNodes(nodes []ast.Node, pos token.Pos) {
	for _, node := range nodes {
		clearNodePositions(node)
	}
	if len(nodes) > 0 {
		setFirstTokenPos(nodes[0], pos)
	}
}

// setFirstTokenPos walks the AST depth-first and sets the position of the first
// positioned token (Ident or BasicLit) to pos.
func setFirstTokenPos(node ast.Node, pos token.Pos) {
	done := false
	ast.Inspect(node, func(n ast.Node) bool {
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
	basicTypes := map[string]bool{
		"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
		"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
		"float32": true, "float64": true,
		"byte": true, "rune": true,
	}

	if !basicTypes[targetType] {
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
