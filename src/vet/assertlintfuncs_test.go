package vet

import (
	"go/ast"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeterminePositiveAssertFunc(t *testing.T) {
	tests := []struct {
		name     string
		cond     ast.Expr
		expected string
	}{
		{
			name: "equal to nil",
			cond: &ast.BinaryExpr{
				X: &ast.Ident{Name: "x"}, Op: token.EQL, Y: &ast.Ident{Name: "nil"},
			},
			expected: "Nil",
		},
		{
			name: "not equal to nil",
			cond: &ast.BinaryExpr{
				X: &ast.Ident{Name: "x"}, Op: token.NEQ, Y: &ast.Ident{Name: "nil"},
			},
			expected: "NotNil",
		},
		{
			name: "equal values",
			cond: &ast.BinaryExpr{
				X: &ast.Ident{Name: "x"}, Op: token.EQL, Y: &ast.Ident{Name: "y"},
			},
			expected: "Equal",
		},
		{
			name: "less than",
			cond: &ast.BinaryExpr{
				X: &ast.Ident{Name: "x"}, Op: token.LSS, Y: &ast.Ident{Name: "y"},
			},
			expected: "Less",
		},
		{
			name: "greater than",
			cond: &ast.BinaryExpr{
				X: &ast.Ident{Name: "x"}, Op: token.GTR, Y: &ast.Ident{Name: "y"},
			},
			expected: "Greater",
		},
		{
			name: "less or equal",
			cond: &ast.BinaryExpr{
				X: &ast.Ident{Name: "x"}, Op: token.LEQ, Y: &ast.Ident{Name: "y"},
			},
			expected: "LessOrEqual",
		},
		{
			name: "greater or equal",
			cond: &ast.BinaryExpr{
				X: &ast.Ident{Name: "x"}, Op: token.GEQ, Y: &ast.Ident{Name: "y"},
			},
			expected: "GreaterOrEqual",
		},
		{
			name:     "simple identifier",
			cond:     &ast.Ident{Name: "ok"},
			expected: "True",
		},
		{
			name:     "parenthesized",
			cond:     &ast.ParenExpr{X: &ast.Ident{Name: "ok"}},
			expected: "True",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, determinePositiveAssertFunc(tt.cond))
		})
	}
}

func TestDetermineNegativeAssertFunc(t *testing.T) {
	tests := []struct {
		name     string
		cond     ast.Expr
		expected string
	}{
		{
			name: "equal to nil -> NotNil",
			cond: &ast.BinaryExpr{
				X: &ast.Ident{Name: "x"}, Op: token.EQL, Y: &ast.Ident{Name: "nil"},
			},
			expected: "NotNil",
		},
		{
			name: "not equal to nil -> Nil",
			cond: &ast.BinaryExpr{
				X: &ast.Ident{Name: "x"}, Op: token.NEQ, Y: &ast.Ident{Name: "nil"},
			},
			expected: "Nil",
		},
		{
			name: "less than -> GreaterOrEqual",
			cond: &ast.BinaryExpr{
				X: &ast.Ident{Name: "x"}, Op: token.LSS, Y: &ast.Ident{Name: "y"},
			},
			expected: "GreaterOrEqual",
		},
		{
			name: "greater than -> LessOrEqual",
			cond: &ast.BinaryExpr{
				X: &ast.Ident{Name: "x"}, Op: token.GTR, Y: &ast.Ident{Name: "y"},
			},
			expected: "LessOrEqual",
		},
		{
			name:     "simple identifier -> False",
			cond:     &ast.Ident{Name: "ok"},
			expected: "False",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, determineNegativeAssertFunc(tt.cond))
		})
	}
}

func TestIsNil(t *testing.T) {
	assert.True(t, isNil(&ast.Ident{Name: "nil"}))
	assert.False(t, isNil(&ast.Ident{Name: "x"}))
	assert.False(t, isNil(&ast.BasicLit{Kind: token.INT, Value: "0"}))
}

func TestGetCallFuncName(t *testing.T) {
	// strings.Contains(a, b)
	call := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   &ast.Ident{Name: "strings"},
			Sel: &ast.Ident{Name: "Contains"},
		},
	}
	assert.Equal(t, "strings.Contains", getCallFuncName(call))

	// plain function call - no package
	call2 := &ast.CallExpr{Fun: &ast.Ident{Name: "foo"}}
	assert.Equal(t, "", getCallFuncName(call2))
}

func TestHasTestingErrorCall(t *testing.T) {
	// Block with t.Error
	block := &ast.BlockStmt{
		List: []ast.Stmt{
			&ast.ExprStmt{
				X: &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   &ast.Ident{Name: "t"},
						Sel: &ast.Ident{Name: "Error"},
					},
				},
			},
		},
	}
	assert.True(t, hasTestingErrorCall(block))

	// Empty block
	assert.False(t, hasTestingErrorCall(&ast.BlockStmt{}))
	assert.False(t, hasTestingErrorCall(nil))
}

func TestIsTestingErrorCall(t *testing.T) {
	tests := []struct {
		receiver string
		method   string
		expected bool
	}{
		{"t", "Error", true},
		{"t", "Errorf", true},
		{"t", "Fatal", true},
		{"t", "Fatalf", true},
		{"b", "Error", true},
		{"t", "Log", false},
		{"x", "Error", false},
	}

	for _, tt := range tests {
		call := &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   &ast.Ident{Name: tt.receiver},
				Sel: &ast.Ident{Name: tt.method},
			},
		}
		assert.Equal(t, tt.expected, isTestingErrorCall(call), "%s.%s", tt.receiver, tt.method)
	}

	// Non-selector call
	call := &ast.CallExpr{Fun: &ast.Ident{Name: "foo"}}
	assert.False(t, isTestingErrorCall(call))
}

func TestGetTestVarName(t *testing.T) {
	block := &ast.BlockStmt{
		List: []ast.Stmt{
			&ast.ExprStmt{
				X: &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   &ast.Ident{Name: "t"},
						Sel: &ast.Ident{Name: "Error"},
					},
				},
			},
		},
	}
	assert.Equal(t, "t", getTestVarName(block))

	// Empty block
	assert.Equal(t, "", getTestVarName(&ast.BlockStmt{}))
}
