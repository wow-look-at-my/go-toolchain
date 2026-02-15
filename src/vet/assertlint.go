package vet

import (
	"bytes"
	"fmt"
	"go/ast"
	//"go/format"
	"go/token"
	"os"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// AssertLintAnalyzer detects manual assertion patterns that should use helper functions.
var AssertLintAnalyzer = &analysis.Analyzer{
	Name: "assertlint",
	Doc:  "detects manual assertion patterns that should use helper functions",
	Run:  runAssertLint,
}

func runAssertLint(pass *analysis.Pass) (any, error) {
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
			if imp.Path.Value == `"github.com/stretchr/testify/assert"` {
				hasAssert = true
			}
			if imp.Path.Value == `"github.com/stretchr/testify/require"` {
				hasRequire = true
			}
		}

		// Collect all diagnostics for this file
		var diagnostics []fileDiagnostic

		ast.Inspect(file, func(n ast.Node) bool {
			ifStmt, ok := n.(*ast.IfStmt)
			if !ok {
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

		// Generate import edit if needed
		var importEdit *analysis.TextEdit
		if (needsAssert && !hasAssert) || (needsRequire && !hasRequire) {
			importEdit = generateImportEdit(pass, file, needsAssert && !hasAssert, needsRequire && !hasRequire)
		}

		// Report all diagnostics with fixes
		for _, d := range diagnostics {
			message := fmt.Sprintf("use %s.%s instead of if + t.Error/t.Fatal", d.assertPkg, d.assertFunc)

			fix := generateSuggestedFix(pass, d.ifStmt, d.assertPkg, d.assertFunc)
			if fix != nil {
				// Add import edit to the first fix only
				if importEdit != nil {
					fix.TextEdits = append(fix.TextEdits, *importEdit)
					importEdit = nil // Only add once
				}
				pass.Report(analysis.Diagnostic{
					Pos:            d.ifStmt.Pos(),
					End:            d.ifStmt.End(),
					Message:        message,
					SuggestedFixes: []analysis.SuggestedFix{*fix},
				})
			} else {
				pass.Reportf(d.ifStmt.Pos(), "%s", message)
			}
		}
	}
	return nil, nil
}

type fileDiagnostic struct {
	ifStmt     *ast.IfStmt
	assertPkg  string
	assertFunc string
}

// generateImportEdit creates a TextEdit to add the testify imports.
func generateImportEdit(pass *analysis.Pass, file *ast.File, addAssert, addRequire bool) *analysis.TextEdit {
	var imports []string
	if addAssert {
		imports = append(imports, `"github.com/stretchr/testify/assert"`)
	}
	if addRequire {
		imports = append(imports, `"github.com/stretchr/testify/require"`)
	}

	if len(imports) == 0 {
		return nil
	}

	// Find where to insert imports
	if len(file.Imports) > 0 {
		// Add after the last import
		lastImport := file.Imports[len(file.Imports)-1]
		newText := "\n"
		for _, imp := range imports {
			newText += "\t" + imp + "\n"
		}
		return &analysis.TextEdit{
			Pos:     lastImport.End(),
			End:     lastImport.End(),
			NewText: []byte(newText),
		}
	}

	// No imports exist, create import block after package declaration
	newImport := &ast.GenDecl{
		Tok: token.IMPORT,
	}
	for _, imp := range imports {
		newImport.Specs = append(newImport.Specs, &ast.ImportSpec{
			Path: &ast.BasicLit{Kind: token.STRING, Value: imp},
		})
	}

	var buf bytes.Buffer
	buf.WriteString("\n\nimport (\n")
	for _, imp := range imports {
		buf.WriteString("\t" + imp + "\n")
	}
	buf.WriteString(")\n")

	return &analysis.TextEdit{
		Pos:     file.Name.End(),
		End:     file.Name.End(),
		NewText: buf.Bytes(),
	}
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

// generateSuggestedFix creates a SuggestedFix for the if statement.
func generateSuggestedFix(pass *analysis.Pass, ifStmt *ast.IfStmt, assertPkg, assertFunc string) *analysis.SuggestedFix {
	// Get the test variable name (t or b)
	tVar := getTestVarName(ifStmt.Body)
	if tVar == "" {
		return nil
	}

	// Generate the replacement text
	replacement := generateReplacement(pass, ifStmt.Cond, tVar, assertPkg, assertFunc)
	if replacement == "" {
		return nil
	}

	return &analysis.SuggestedFix{
		Message: fmt.Sprintf("replace with %s.%s", assertPkg, assertFunc),
		TextEdits: []analysis.TextEdit{
			{
				Pos:     ifStmt.Pos(),
				End:     ifStmt.End(),
				NewText: []byte(replacement),
			},
		},
	}
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

// generateReplacement creates the replacement assertion code.
func generateReplacement(pass *analysis.Pass, cond ast.Expr, tVar, assertPkg, assertFunc string) string {
	// Handle negation
	actualCond := cond
	if unary, ok := cond.(*ast.UnaryExpr); ok && unary.Op == token.NOT {
		actualCond = unary.X
	}

	switch c := actualCond.(type) {
	case *ast.CallExpr:
		return generateCallReplacement(pass, c, tVar, assertPkg, assertFunc)

	case *ast.BinaryExpr:
		return generateBinaryReplacement(pass, c, tVar, assertPkg, assertFunc)

	case *ast.Ident:
		return fmt.Sprintf("%s.%s(%s, %s)", assertPkg, assertFunc, tVar, c.Name)
	}

	// Fallback: use the original condition text
	condText := sourceText(pass, cond)
	return fmt.Sprintf("%s.%s(%s, %s)", assertPkg, assertFunc, tVar, condText)
}

// generateCallReplacement generates replacement for call expressions.
func generateCallReplacement(pass *analysis.Pass, call *ast.CallExpr, tVar, assertPkg, assertFunc string) string {
	funcName := getCallFuncName(call)

	switch funcName {
	case "strings.Contains":
		if len(call.Args) == 2 {
			haystack := sourceText(pass, call.Args[0])
			needle := sourceText(pass, call.Args[1])
			return fmt.Sprintf("%s.%s(%s, %s, %s)", assertPkg, assertFunc, tVar, haystack, needle)
		}

	case "strings.HasPrefix", "strings.HasSuffix":
		if len(call.Args) == 2 {
			s := sourceText(pass, call.Args[0])
			fix := sourceText(pass, call.Args[1])
			// No direct assert function, wrap in True/False
			return fmt.Sprintf("%s.%s(%s, %s(%s, %s))", assertPkg, assertFunc, tVar, funcName, s, fix)
		}

	case "reflect.DeepEqual":
		if len(call.Args) == 2 {
			a := sourceText(pass, call.Args[0])
			b := sourceText(pass, call.Args[1])
			return fmt.Sprintf("%s.%s(%s, %s, %s)", assertPkg, assertFunc, tVar, a, b)
		}
	}

	return ""
}

// generateBinaryReplacement generates replacement for binary expressions.
func generateBinaryReplacement(pass *analysis.Pass, bin *ast.BinaryExpr, tVar, assertPkg, assertFunc string) string {
	left := sourceText(pass, bin.X)
	right := sourceText(pass, bin.Y)

	switch assertFunc {
	case "Equal", "NotEqual":
		// For Equal/NotEqual, expected comes first
		return fmt.Sprintf("%s.%s(%s, %s, %s)", assertPkg, assertFunc, tVar, right, left)
	case "Nil", "NotNil":
		// For Nil/NotNil, only the value
		return fmt.Sprintf("%s.%s(%s, %s)", assertPkg, assertFunc, tVar, left)
	case "Less", "Greater", "LessOrEqual", "GreaterOrEqual":
		return fmt.Sprintf("%s.%s(%s, %s, %s)", assertPkg, assertFunc, tVar, left, right)
	}

	return fmt.Sprintf("%s.%s(%s, %s, %s)", assertPkg, assertFunc, tVar, left, right)
}

// sourceText extracts the source text for a node.
func sourceText(pass *analysis.Pass, node ast.Expr) string {
	start := pass.Fset.Position(node.Pos())
	end := pass.Fset.Position(node.End())
	content, err := os.ReadFile(start.Filename)
	if err != nil {
		return ""
	}
	return string(content[start.Offset:end.Offset])
}
