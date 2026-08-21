package vet

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClearNodePositionsIdent(t *testing.T) {
	ident := &ast.Ident{NamePos: 42, Name: "foo"}
	clearNodePositions(ident)
	assert.Equal(t, token.NoPos, ident.NamePos)
	assert.Equal(t, "foo", ident.Name) // name preserved
}

func TestClearNodePositionsBasicLit(t *testing.T) {
	lit := &ast.BasicLit{ValuePos: 99, Kind: token.INT, Value: "42"}
	clearNodePositions(lit)
	assert.Equal(t, token.NoPos, lit.ValuePos)
	assert.Equal(t, "42", lit.Value)
}

func TestClearNodePositionsBinaryExpr(t *testing.T) {
	expr := &ast.BinaryExpr{
		X:     &ast.Ident{NamePos: 10, Name: "a"},
		OpPos: 20,
		Op:    token.ADD,
		Y:     &ast.Ident{NamePos: 30, Name: "b"},
	}
	clearNodePositions(expr)
	assert.Equal(t, token.NoPos, expr.OpPos)
	assert.Equal(t, token.NoPos, expr.X.(*ast.Ident).NamePos)
	assert.Equal(t, token.NoPos, expr.Y.(*ast.Ident).NamePos)
}

func TestClearNodePositionsCallExpr(t *testing.T) {
	call := &ast.CallExpr{
		Fun:      &ast.Ident{NamePos: 5, Name: "foo"},
		Lparen:   10,
		Rparen:   20,
		Ellipsis: 15,
	}
	clearNodePositions(call)
	assert.Equal(t, token.NoPos, call.Lparen)
	assert.Equal(t, token.NoPos, call.Rparen)
	assert.Equal(t, token.NoPos, call.Ellipsis)
}

func TestClearNodePositionsParenExpr(t *testing.T) {
	paren := &ast.ParenExpr{
		Lparen: 1,
		Rparen: 5,
		X:      &ast.Ident{NamePos: 3, Name: "x"},
	}
	clearNodePositions(paren)
	assert.Equal(t, token.NoPos, paren.Lparen)
	assert.Equal(t, token.NoPos, paren.Rparen)
}

func TestClearNodePositionsUnaryExpr(t *testing.T) {
	unary := &ast.UnaryExpr{OpPos: 1, Op: token.NOT, X: &ast.Ident{Name: "x"}}
	clearNodePositions(unary)
	assert.Equal(t, token.NoPos, unary.OpPos)
}

func TestClearNodePositionsIndexExpr(t *testing.T) {
	idx := &ast.IndexExpr{Lbrack: 5, Rbrack: 10}
	clearNodePositions(idx)
	assert.Equal(t, token.NoPos, idx.Lbrack)
	assert.Equal(t, token.NoPos, idx.Rbrack)
}

func TestClearNodePositionsStarExpr(t *testing.T) {
	star := &ast.StarExpr{Star: 5, X: &ast.Ident{Name: "Foo"}}
	clearNodePositions(star)
	assert.Equal(t, token.NoPos, star.Star)
}

func TestClearNodePositionsCompositeLit(t *testing.T) {
	cl := &ast.CompositeLit{Lbrace: 1, Rbrace: 10}
	clearNodePositions(cl)
	assert.Equal(t, token.NoPos, cl.Lbrace)
	assert.Equal(t, token.NoPos, cl.Rbrace)
}

func TestClearNodePositionsKeyValueExpr(t *testing.T) {
	kv := &ast.KeyValueExpr{
		Key:   &ast.Ident{Name: "k"},
		Colon: 5,
		Value: &ast.Ident{Name: "v"},
	}
	clearNodePositions(kv)
	assert.Equal(t, token.NoPos, kv.Colon)
}

func TestClearNodePositionsSliceExpr(t *testing.T) {
	sl := &ast.SliceExpr{Lbrack: 3, Rbrack: 8}
	clearNodePositions(sl)
	assert.Equal(t, token.NoPos, sl.Lbrack)
	assert.Equal(t, token.NoPos, sl.Rbrack)
}

func TestClearNodePositionsTypeAssertExpr(t *testing.T) {
	ta := &ast.TypeAssertExpr{Lparen: 1, Rparen: 5}
	clearNodePositions(ta)
	assert.Equal(t, token.NoPos, ta.Lparen)
	assert.Equal(t, token.NoPos, ta.Rparen)
}

func TestClearNodePositionsAssignStmt(t *testing.T) {
	as := &ast.AssignStmt{TokPos: 10, Tok: token.ASSIGN}
	clearNodePositions(as)
	assert.Equal(t, token.NoPos, as.TokPos)
}

func TestClearNodePositionsComplex(t *testing.T) {
	// Parse a real expression to get a complex AST
	fset := token.NewFileSet()
	expr, err := parser.ParseExpr("a + b*c")
	require.NoError(t, err)
	_ = fset

	// All positions should be non-zero initially
	clearNodePositions(expr)

	// Walk and verify all positions are cleared
	ast.Inspect(expr, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		switch x := n.(type) {
		case *ast.Ident:
			assert.Equal(t, token.NoPos, x.NamePos, "ident %s", x.Name)
		case *ast.BinaryExpr:
			assert.Equal(t, token.NoPos, x.OpPos)
		}
		return true
	})
}

func TestPrepareFixNodes(t *testing.T) {
	nodes := []ast.Node{
		&ast.Ident{NamePos: 100, Name: "a"},
		&ast.BasicLit{ValuePos: 200, Value: "42"},
	}
	prepareFixNodes(nodes, 50)

	// First node should have its position set to 50
	assert.Equal(t, token.Pos(50), nodes[0].(*ast.Ident).NamePos)
	// Second node should have positions cleared
	assert.Equal(t, token.NoPos, nodes[1].(*ast.BasicLit).ValuePos)
}

func TestPrepareFixNodesEmpty(t *testing.T) {
	// Should not panic
	prepareFixNodes(nil, 50)
	prepareFixNodes([]ast.Node{}, 50)
}
