// nilnode.go guards the one way ast.Inspect panics on a tree that parsed fine.
//
// Half the node fields in go/ast are legitimately absent: a FuncDecl.Body a
// function implemented in assembly never had, an IfStmt.Else with no else, a
// ForStmt.Init of a bare loop. Each is a nil POINTER, and handing one to
// ast.Inspect wraps it in an interface that is not nil. ast.Walk switches on
// the dynamic type, matches, and dereferences. The callback's own `n == nil`
// guard cannot help: it never runs.
package vet

import (
	"go/ast"
	"reflect"
)

// InspectNode walks node, and does nothing when the node is absent. Every other
// argument behaves as ast.Inspect.
func InspectNode(node ast.Node, visit func(ast.Node) bool) {
	if isAbsent(node) {
		return
	}
	ast.Inspect(node, visit)
}

// isAbsent reports whether node is a nil interface or an interface holding a
// nil pointer. The second is what a nilable AST field gives you.
func isAbsent(node ast.Node) bool {
	if node == nil {
		return true
	}
	held := reflect.ValueOf(node)
	return held.Kind() == reflect.Pointer && held.IsNil()
}
