package vet

import (
	"fmt"
	"go/ast"
	"go/token"
	"reflect"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/astutil"
)

// AssertLintAnalyzer detects manual assertion patterns that should use helper functions.
var AssertLintAnalyzer = &analysis.Analyzer{
	Name:       "assertlint",
	Doc:        "detects manual assertion patterns that should use helper functions",
	Run:        runAssertLint,
	ResultType: reflect.TypeOf([]*ASTFixes{}),
}

func runAssertLint(pass *analysis.Pass) (any, error) {
	// Group fixes by file
	fileToFixes := make(map[*ast.File][]ASTFix)

	for _, file := range pass.Files {
		// Only check test files
		filename := pass.Fset.File(file.Pos()).Name()
		if !strings.HasSuffix(filename, "_test.go") {
			continue
		}

		// Track needed imports for this file
		needsAssert := false
		needsRequire := false

		// Check existing imports
		hasAssert := false
		hasRequire := false
		for _, imp := range file.Imports {
			if imp.Path.Value == `"github.com/wow-look-at-my/testify/assert"` {
				hasAssert = true
			}
			if imp.Path.Value == `"github.com/wow-look-at-my/testify/require"` {
				hasRequire = true
			}
		}

		// Collect all diagnostics for this file
		var diagnostics []fileDiagnostic

		// Build set of "else if" statements (if statements that are the Else of another if)
		elseIfStmts := make(map[*ast.IfStmt]bool)
		ast.Inspect(file, func(n ast.Node) bool {
			ifStmt, ok := n.(*ast.IfStmt)
			if !ok {
				return true
			}
			if elseIf, ok := ifStmt.Else.(*ast.IfStmt); ok {
				elseIfStmts[elseIf] = true
			}
			return true
		})

		ast.Inspect(file, func(n ast.Node) bool {
			ifStmt, ok := n.(*ast.IfStmt)
			if !ok {
				return true
			}

			// Skip "else if" statements - they can't be auto-fixed safely
			if elseIfStmts[ifStmt] {
				return true
			}

			// Check if the body contains t.Error/t.Errorf/t.Fatal/t.Fatalf
			if !hasTestingErrorCall(ifStmt.Body) {
				return true
			}

			// Determine the assertion type (assert vs require) and function name
			assertPkg, assertFunc := determineAssertion(ifStmt)

			if assertPkg == "assert" {
				needsAssert = true
			} else {
				needsRequire = true
			}

			diagnostics = append(diagnostics, fileDiagnostic{
				ifStmt:     ifStmt,
				assertPkg:  assertPkg,
				assertFunc: assertFunc,
			})

			return true
		})

		// Process diagnostics and generate AST fixes
		for _, d := range diagnostics {
			message := fmt.Sprintf("use %s.%s instead of if + t.Error/t.Fatal", d.assertPkg, d.assertFunc)

			fix := generateASTFix(pass, d.ifStmt, d.assertPkg, d.assertFunc)
			if fix != nil {
				fileToFixes[file] = append(fileToFixes[file], *fix)
			}
			// Always report diagnostic (without SuggestedFixes - AST fixes handle that)
			pass.Reportf(d.ifStmt.Pos(), "%s", message)
		}

		// Add imports directly to the AST if needed
		if (needsAssert && !hasAssert) || (needsRequire && !hasRequire) {
			if needsAssert && !hasAssert {
				astutil.AddImport(pass.Fset, file, "github.com/wow-look-at-my/testify/assert")
			}
			if needsRequire && !hasRequire {
				astutil.AddImport(pass.Fset, file, "github.com/wow-look-at-my/testify/require")
			}
		}
	}

	if len(fileToFixes) == 0 {
		return []*ASTFixes(nil), nil
	}

	// Return all files' fixes
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

type fileDiagnostic struct {
	ifStmt     *ast.IfStmt
	assertPkg  string
	assertFunc string
}

// hasTestingErrorCall checks if the block contains a call to t.Error, t.Errorf, t.Fatal, or t.Fatalf.
func hasTestingErrorCall(block *ast.BlockStmt) bool {
	if block == nil {
		return false
	}

	for _, stmt := range block.List {
		exprStmt, ok := stmt.(*ast.ExprStmt)
		if !ok {
			continue
		}

		call, ok := exprStmt.X.(*ast.CallExpr)
		if !ok {
			continue
		}

		if isTestingErrorCall(call) {
			return true
		}
	}
	return false
}

// isTestingErrorCall returns true if the call is t.Error, t.Errorf, t.Fatal, or t.Fatalf.
func isTestingErrorCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	// Check if it's a method call on an identifier (like t.Error)
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}

	// Common test receiver names
	if ident.Name != "t" && ident.Name != "b" {
		return false
	}

	// Check method name
	switch sel.Sel.Name {
	case "Error", "Errorf", "Fatal", "Fatalf":
		return true
	}
	return false
}

// determineAssertion analyzes the if statement and returns the appropriate package and function.
// Returns (assertPkg, assertFunc) like ("assert", "Contains") or ("require", "NoError").
func determineAssertion(ifStmt *ast.IfStmt) (string, string) {
	// Determine assert vs require based on t.Error vs t.Fatal
	assertPkg := "assert"
	for _, stmt := range ifStmt.Body.List {
		if exprStmt, ok := stmt.(*ast.ExprStmt); ok {
			if call, ok := exprStmt.X.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					if sel.Sel.Name == "Fatal" || sel.Sel.Name == "Fatalf" {
						assertPkg = "require"
						break
					}
				}
			}
		}
	}

	// Check for init clause error pattern: if err := X; err != nil
	if ifStmt.Init != nil {
		if assign, ok := ifStmt.Init.(*ast.AssignStmt); ok && assign.Tok == token.DEFINE {
			if len(assign.Lhs) == 1 && len(assign.Rhs) == 1 {
				if errVar, ok := assign.Lhs[0].(*ast.Ident); ok {
					if binExpr, ok := ifStmt.Cond.(*ast.BinaryExpr); ok && binExpr.Op == token.NEQ {
						if condVar, ok := binExpr.X.(*ast.Ident); ok && condVar.Name == errVar.Name && isNil(binExpr.Y) {
							return assertPkg, "NoError"
						}
					}
				}
			}
		}
	}

	// Determine the function based on the condition
	assertFunc := determineAssertFunc(ifStmt.Cond)

	return assertPkg, assertFunc
}

// determineAssertFunc analyzes the condition and returns the appropriate assert function name.
func determineAssertFunc(cond ast.Expr) string {
	// Handle negation: if !cond { t.Error }
	if unary, ok := cond.(*ast.UnaryExpr); ok && unary.Op == token.NOT {
		return determinePositiveAssertFunc(unary.X)
	}

	// Handle direct condition: if cond { t.Error } -> need False/negative assertion
	return determineNegativeAssertFunc(cond)
}

// determinePositiveAssertFunc returns the assert function for: if !cond { t.Error }
// This means we want to assert that cond IS true.
func determinePositiveAssertFunc(cond ast.Expr) string {
	switch c := cond.(type) {
	case *ast.ParenExpr:
		return determinePositiveAssertFunc(c.X)

	case *ast.CallExpr:
		if funcName := getCallFuncName(c); funcName != "" {
			switch funcName {
			case "strings.Contains":
				return "Contains"
			case "strings.HasPrefix":
				return "True" // No direct HasPrefix
			case "strings.HasSuffix":
				return "True" // No direct HasSuffix
			case "reflect.DeepEqual":
				return "Equal"
			}
		}

	case *ast.BinaryExpr:
		switch c.Op {
		case token.EQL:
			// Check for nil comparison
			if isNil(c.Y) {
				return "Nil"
			}
			return "Equal"
		case token.NEQ:
			if isNil(c.Y) {
				return "NotNil"
			}
			return "NotEqual"
		case token.LSS:
			return "Less"
		case token.GTR:
			return "Greater"
		case token.LEQ:
			return "LessOrEqual"
		case token.GEQ:
			return "GreaterOrEqual"
		}

	case *ast.Ident:
		return "True"
	}

	return "True"
}

// determineNegativeAssertFunc returns the assert function for: if cond { t.Error }
// This means we want to assert that cond is NOT true.
func determineNegativeAssertFunc(cond ast.Expr) string {
	switch c := cond.(type) {
	case *ast.ParenExpr:
		return determineNegativeAssertFunc(c.X)

	case *ast.CallExpr:
		if funcName := getCallFuncName(c); funcName != "" {
			switch funcName {
			case "strings.Contains":
				return "NotContains"
			case "reflect.DeepEqual":
				return "NotEqual"
			}
		}

	case *ast.BinaryExpr:
		switch c.Op {
		case token.EQL:
			if isNil(c.Y) {
				return "NotNil"
			}
			return "NotEqual"
		case token.NEQ:
			if isNil(c.Y) {
				return "Nil"
			}
			return "Equal"
		case token.LSS:
			return "GreaterOrEqual"
		case token.GTR:
			return "LessOrEqual"
		case token.LEQ:
			return "Greater"
		case token.GEQ:
			return "Less"
		}

	case *ast.Ident:
		return "False"
	}

	return "False"
}

// getCallFuncName returns the qualified function name like "strings.Contains".
func getCallFuncName(call *ast.CallExpr) string {
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		if pkg, ok := sel.X.(*ast.Ident); ok {
			return pkg.Name + "." + sel.Sel.Name
		}
	}
	return ""
}

// isNil checks if the expression is the nil identifier.
func isNil(expr ast.Expr) bool {
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name == "nil"
	}
	return false
}

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
					return &ASTFix{
						OldNode:  ifStmt,
						NewNodes: []ast.Node{&ast.ExprStmt{X: noErrorCall}},
					}
				}
			}
		}

		// General case: extract init statement
		return &ASTFix{
			OldNode:  ifStmt,
			NewNodes: []ast.Node{ifStmt.Init, assertStmt},
		}
	}

	// Simple case: if cond { t.Error } → assert.X(t, ...)
	return &ASTFix{
		OldNode:  ifStmt,
		NewNodes: []ast.Node{assertStmt},
	}
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

	// Fallback: wrap the original condition
	return makeCall(makeSelector(assertPkg, assertFunc), ast.NewIdent(tVar), cond)
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

