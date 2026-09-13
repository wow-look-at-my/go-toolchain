package vet

import (
	"go/ast"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A nilable AST field handed straight to ast.Inspect is the shape that panics.
// The interface is not nil -- it holds a nil pointer -- so the callback's own
// `n == nil` guard never runs, and ast.Walk dereferences the field first.
func TestInspectPanicsOnATypedNilNode(test *testing.T) {
	var missing *ast.BlockStmt

	// Compared with ==, which is the comparison a callback's guard performs.
	// Reflection-based helpers call this nil and would hide the whole defect.
	assert.False(test, ast.Node(missing) == nil, "a nil pointer in an interface is not a nil interface")
	assert.Panics(test, func() {
		ast.Inspect(missing, func(node ast.Node) bool { return node != nil })
	}, "this is the panic the guarded callback cannot stop")
}

// InspectNode is the guard, so a caller may hand it a field that is legitimately
// absent: a FuncDecl.Body of a function implemented in assembly, an IfStmt.Else
// with no else, a ForStmt.Init of a bare loop.
func TestInspectNodeTakesWhatIsAbsent(test *testing.T) {
	for _, row := range []struct {
		name string
		node ast.Node
	}{
		{"a nil interface", nil},
		{"a body a declaration never had", (*ast.BlockStmt)(nil)},
		{"an else that is not written", (*ast.IfStmt)(nil)},
		{"an expression a statement omits", (*ast.Ident)(nil)},
	} {
		test.Run(row.name, func(test *testing.T) {
			visited := 0
			assert.NotPanics(test, func() {
				InspectNode(row.node, func(ast.Node) bool { visited++; return true })
			})
			assert.Zero(test, visited, "there is nothing under an absent node to visit")
		})
	}
}

// What is present still gets walked, or the guard would be a way to skip work.
func TestInspectNodeStillWalksWhatIsThere(test *testing.T) {
	body := &ast.BlockStmt{List: []ast.Stmt{
		&ast.ExprStmt{X: ast.NewIdent("first")},
		&ast.ExprStmt{X: ast.NewIdent("second")},
	}}

	var names []string
	InspectNode(body, func(node ast.Node) bool {
		if ident, isIdent := node.(*ast.Ident); isIdent {
			names = append(names, ident.Name)
		}
		return true
	})

	require.Len(test, names, 2)
	assert.Equal(test, []string{"first", "second"}, names)
}
