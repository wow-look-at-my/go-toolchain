package test

import (
	"go/ast"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
)

func TestReceiverTypeIdent(t *testing.T) {
	ident := &ast.Ident{Name: "Foo"}
	assert.Equal(t, "Foo", receiverType(ident))
}

func TestReceiverTypePointer(t *testing.T) {
	star := &ast.StarExpr{X: &ast.Ident{Name: "Bar"}}
	assert.Equal(t, "Bar", receiverType(star))
}

func TestReceiverTypeDoublePointer(t *testing.T) {
	star := &ast.StarExpr{X: &ast.StarExpr{X: &ast.Ident{Name: "Baz"}}}
	assert.Equal(t, "Baz", receiverType(star))
}

func TestReceiverTypeOther(t *testing.T) {
	// Non-Ident, non-StarExpr returns ""
	arr := &ast.ArrayType{Elt: &ast.Ident{Name: "int"}}
	assert.Equal(t, "", receiverType(arr))
}

func TestReceiverTypeIndexExpr(t *testing.T) {
	// Generic type expression — not handled, returns ""
	idx := &ast.IndexExpr{X: &ast.Ident{Name: "Generic"}}
	assert.Equal(t, "", receiverType(idx))
}
