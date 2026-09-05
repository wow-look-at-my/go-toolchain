package vet

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestASTFixesFprint(t *testing.T) {
	t.Serial()
	before := `package main

func main() {
	x := int(0)
	_ = x
}
`
	after := `package main

func main() {
	x := 0
	_ = x
}
`

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", before, parser.ParseComments)
	require.Nil(t, err)

	// Find the redundant int conversion
	var call *ast.CallExpr
	ast.Inspect(f, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "int" {
				call = c
				return false
			}
		}
		return true
	})
	require.NotNil(t, call)

	fixes := &ASTFixes{File: f, Fset: fset, Fixes: []ASTFix{{OldNode: call, NewNodes: []ast.Node{call.Args[0]}}}}

	var buf strings.Builder
	err = fixes.Fprint(&buf)
	assert.Nil(t, err)
	assert.Equal(t, after, buf.String())
}

func TestASTFixesFprintMultiple(t *testing.T) {
	t.Serial()
	before := `package main

func main() {
	x := int(0)
	y := int(1)
	_ = x
	_ = y
}
`
	after := `package main

func main() {
	x := 0
	y := 1
	_ = x
	_ = y
}
`

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", before, parser.ParseComments)
	require.Nil(t, err)

	var fixes []ASTFix
	ast.Inspect(f, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "int" {
				fixes = append(fixes, ASTFix{OldNode: c, NewNodes: []ast.Node{c.Args[0]}})
			}
		}
		return true
	})
	require.Len(t, fixes, 2)

	astFixes := &ASTFixes{File: f, Fset: fset, Fixes: fixes}

	var buf strings.Builder
	err = astFixes.Fprint(&buf)
	assert.Nil(t, err)
	assert.Equal(t, after, buf.String())
}

func TestASTFixesPrintFix(t *testing.T) {
	t.Serial()
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "test.go", `package main; func main() { x := int(0); _ = x }`, 0)

	var call *ast.CallExpr
	ast.Inspect(f, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			call = c
			return false
		}
		return true
	})

	fixes := &ASTFixes{File: f, Fset: fset, Fixes: []ASTFix{
		{OldNode: call, NewNodes: []ast.Node{call.Args[0]}}, // replacement
		{OldNode: call, NewNodes: nil},                      // deletion
	}}

	// Just ensure printFix doesn't panic
	for _, fix := range fixes.Fixes {
		fixes.printFix(fix)
	}
}
