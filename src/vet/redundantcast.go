package vet

import (
	"fmt"
	"go/ast"
	"go/token"
	"reflect"

	"golang.org/x/tools/go/analysis"
)

// RedundantCastAnalyzer detects unnecessary type casts on literals.
// e.g., int(0), float64(1.5), string("hello")
var RedundantCastAnalyzer = &analysis.Analyzer{
	Name:       "redundantcast",
	Doc:        "detects unnecessary type casts on literals like int(0) or float64(1.5)",
	Run:        runRedundantCast,
	ResultType: reflect.TypeOf([]*ASTFixes{}),
}

func runRedundantCast(pass *analysis.Pass) (any, error) {
	// Group fixes by file
	fileToFixes := make(map[*ast.File][]ASTFix)

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
				pass.Reportf(call.Pos(), "redundant cast: %s(%s) can be just %s", typeIdent.Name, lit.Value, lit.Value)
				fileToFixes[file] = append(fileToFixes[file], ASTFix{
					OldNode: call,
					NewNode: lit,
				})
			}

			return true
		})
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
