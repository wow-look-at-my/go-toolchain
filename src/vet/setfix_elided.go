// spellElidedType writes out the type a composite literal element elided.

package vet

import (
	"go/ast"
	"go/token"
)

// spellElidedType writes out the type a composite literal element left to its
// container: in `[][4]byte{{1, 2, 3, 4}}` the element is `{1, 2, 3, 4}`. As a
// call argument it needs its type, `[4]byte{1, 2, 3, 4}`. An element of
// pointer type `*T` elides `&T`, so it becomes `&T{...}`.
func spellElidedType(e, elemType ast.Expr) ast.Expr {
	lit, ok := e.(*ast.CompositeLit)
	if !ok || lit.Type != nil {
		return e
	}
	if star, ok := elemType.(*ast.StarExpr); ok {
		typed := *lit
		typed.Type = star.X
		return &ast.UnaryExpr{OpPos: lit.Lbrace, Op: token.AND, X: &typed}
	}
	typed := *lit
	typed.Type = elemType
	return &typed
}
