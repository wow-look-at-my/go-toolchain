package vet

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"

	"golang.org/x/tools/go/analysis"
)

// RedundantCastAnalyzer detects unnecessary type casts on literals.
// e.g., int(0), float64(1.5), string("hello")
var RedundantCastAnalyzer = &analysis.Analyzer{
	Name: "redundantcast",
	Doc:  "detects unnecessary type casts on literals like int(0) or float64(1.5)",
	Run:  runRedundantCast,
}

func runRedundantCast(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}

			// Check if it's a type conversion (identifier as function)
			typeIdent, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}

			// Check if the argument is a basic literal
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				return true
			}

			// Check if the cast is redundant
			if isRedundantCast(typeIdent.Name, lit) {
				fix := generateRedundantCastFix(pass, call, lit)
				if fix != nil {
					pass.Report(analysis.Diagnostic{
						Pos:            call.Pos(),
						End:            call.End(),
						Message:        fmt.Sprintf("redundant cast: %s(%s) can be just %s", typeIdent.Name, lit.Value, lit.Value),
						SuggestedFixes: []analysis.SuggestedFix{*fix},
					})
				} else {
					pass.Reportf(call.Pos(), "redundant cast: %s(%s) can be just %s", typeIdent.Name, lit.Value, lit.Value)
				}
			}

			return true
		})
	}
	return nil, nil
}

// isRedundantCast returns true if the cast is to the literal's default type.
func isRedundantCast(typeName string, lit *ast.BasicLit) bool {
	switch lit.Kind {
	case token.INT:
		// Integer literals default to int
		return typeName == "int"
	case token.FLOAT:
		// Float literals default to float64
		return typeName == "float64"
	case token.STRING:
		// String literals are already string
		return typeName == "string"
	case token.CHAR:
		// Rune literals default to rune (alias for int32)
		return typeName == "rune" || typeName == "int32"
	}
	return false
}

// generateRedundantCastFix creates a fix that removes the redundant cast.
func generateRedundantCastFix(pass *analysis.Pass, call *ast.CallExpr, lit *ast.BasicLit) *analysis.SuggestedFix {
	start := pass.Fset.Position(call.Pos())
	content, err := os.ReadFile(start.Filename)
	if err != nil {
		return nil
	}

	// Get just the literal text
	litStart := pass.Fset.Position(lit.Pos())
	litEnd := pass.Fset.Position(lit.End())
	litText := string(content[litStart.Offset:litEnd.Offset])

	return &analysis.SuggestedFix{
		Message: "remove redundant cast",
		TextEdits: []analysis.TextEdit{{
			Pos:     call.Pos(),
			End:     call.End(),
			NewText: []byte(litText),
		}},
	}
}
