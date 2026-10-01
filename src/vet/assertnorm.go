package vet

import (
	"fmt"
	"go/ast"
	"go/token"
	"reflect"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// AssertNormAnalyzer detects assert.True(t, !expr) and rewrites to assert.False(t, expr), and vice versa.
var AssertNormAnalyzer = &analysis.Analyzer{
	Name:       "assertnorm",
	Doc:        "normalizes negated assert.True/False calls",
	Run:        runAssertNorm,
	ResultType: reflect.TypeOf([]*ASTFixes{}),
}

func runAssertNorm(pass *analysis.Pass) (any, error) {
	fileToFixes := make(map[*ast.File][]ASTFix)

	for _, file := range pass.Files {
		filename := pass.Fset.File(file.Pos()).Name()
		if !strings.HasSuffix(filename, "_test.go") {
			continue
		}

		ast.Inspect(file, func(n ast.Node) bool {
			exprStmt, ok := n.(*ast.ExprStmt)
			if !ok {
				return true
			}
			call, ok := exprStmt.X.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if pkg.Name != "assert" && pkg.Name != "require" {
				return true
			}

			var flippedFunc string
			switch sel.Sel.Name {
			case "True":
				flippedFunc = "False"
			case "False":
				flippedFunc = "True"
			default:
				return true
			}

			// Need the (t, expr) pair at minimum — optional msg args may follow
			if len(call.Args) < 2 {
				return true
			}

			// Check if the expr argument is a negation: !expr
			unary, ok := call.Args[1].(*ast.UnaryExpr)
			if !ok || unary.Op != token.NOT {
				return true
			}

			message := fmt.Sprintf("use %s.%s instead of %s.%s with negation", pkg.Name, flippedFunc, pkg.Name, sel.Sel.Name)
			pass.Reportf(call.Pos(), "%s", message)

			// Build fix: flip the function name and unwrap the negation
			newArgs := make([]ast.Expr, len(call.Args))
			copy(newArgs, call.Args)
			newArgs[1] = unary.X

			newCall := &ast.CallExpr{
				Fun:  makeSelector(pkg.Name, flippedFunc),
				Args: newArgs,
			}
			newStmt := &ast.ExprStmt{X: newCall}
			newNodes := []ast.Node{newStmt}
			prepareFixNodes(newNodes, exprStmt.Pos())

			fileToFixes[file] = append(fileToFixes[file], ASTFix{
				OldNode:  exprStmt,
				NewNodes: newNodes,
			})

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
