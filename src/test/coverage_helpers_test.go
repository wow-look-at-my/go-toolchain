package test

import (
	"go/ast"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReceiverTypeIdent(t *testing.T) {
	t.Serial()
	ident := &ast.Ident{Name: "Foo"}
	assert.Equal(t, "Foo", receiverType(ident))
}

func TestReceiverTypePointer(t *testing.T) {
	t.Serial()
	star := &ast.StarExpr{X: &ast.Ident{Name: "Bar"}}
	assert.Equal(t, "Bar", receiverType(star))
}

func TestReceiverTypeDoublePointer(t *testing.T) {
	t.Serial()
	star := &ast.StarExpr{X: &ast.StarExpr{X: &ast.Ident{Name: "Baz"}}}
	assert.Equal(t, "Baz", receiverType(star))
}

func TestReceiverTypeOther(t *testing.T) {
	t.Serial()
	// Non-Ident, non-StarExpr returns ""
	arr := &ast.ArrayType{Elt: &ast.Ident{Name: "int"}}
	assert.Equal(t, "", receiverType(arr))
}

func TestReceiverTypeIndexExpr(t *testing.T) {
	t.Serial()
	// Generic type expression — not handled, returns ""
	idx := &ast.IndexExpr{X: &ast.Ident{Name: "Generic"}}
	assert.Equal(t, "", receiverType(idx))
}
