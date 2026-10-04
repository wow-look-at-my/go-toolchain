package vet

import (
	"go/ast"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCastableType(t *testing.T) {
	t.Serial()
	tests := []struct {
		litKind    token.Token
		litValue   string
		targetType string
		expected   string
	}{
		{token.INT, "42", "int64", "int64"},
		{token.INT, "42", "int", ""},
		{token.INT, "42", "uint32", "uint32"},
		{token.FLOAT, "3.14", "float32", "float32"},
		{token.FLOAT, "3.14", "float64", ""},
		{token.INT, "42", "time.Duration", ""}, // complex type - no cast
		{token.INT, "42", "byte", "byte"},
		{token.INT, "42", "rune", "rune"},
	}

	for _, tt := range tests {
		name := tt.targetType
		t.Run(name, func(t *testing.T) {
			lit := &ast.BasicLit{Kind: tt.litKind, Value: tt.litValue}
			assert.Equal(t, tt.expected, castableType(lit, tt.targetType))
		})
	}
}

func TestDeterminePositiveAssertFunc(t *testing.T) {
	t.Serial()
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
	t.Serial()
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
	t.Serial()
	assert.True(t, isNil(&ast.Ident{Name: "nil"}))
	assert.False(t, isNil(&ast.Ident{Name: "x"}))
	assert.False(t, isNil(&ast.BasicLit{Kind: token.INT, Value: "0"}))
}

func TestGetCallFuncName(t *testing.T) {
	t.Serial()
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
	t.Serial()
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
	t.Serial()
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
	t.Serial()
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

func TestDetermineAssertFuncUnary(t *testing.T) {
	t.Serial()
	// Test negation path
	cond := &ast.UnaryExpr{
		Op: token.NOT,
		X:  &ast.Ident{Name: "ok"},
	}
	assert.Equal(t, "True", determineAssertFunc(cond))

	// Test direct identifier
	assert.Equal(t, "False", determineAssertFunc(&ast.Ident{Name: "ok"}))
}

func TestDeterminePositiveAssertFuncCall(t *testing.T) {
	t.Serial()
	// Test strings.HasPrefix
	call := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   &ast.Ident{Name: "strings"},
			Sel: &ast.Ident{Name: "HasPrefix"},
		},
		Args: []ast.Expr{
			&ast.Ident{Name: "s"},
			&ast.BasicLit{Kind: token.STRING, Value: `"hello"`},
		},
	}
	assert.Equal(t, "True", determinePositiveAssertFunc(call))

	// Test strings.HasSuffix
	call2 := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   &ast.Ident{Name: "strings"},
			Sel: &ast.Ident{Name: "HasSuffix"},
		},
	}
	assert.Equal(t, "True", determinePositiveAssertFunc(call2))

	// Test reflect.DeepEqual
	call3 := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   &ast.Ident{Name: "reflect"},
			Sel: &ast.Ident{Name: "DeepEqual"},
		},
	}
	assert.Equal(t, "Equal", determinePositiveAssertFunc(call3))
}

func TestDetermineNegativeAssertFuncCall(t *testing.T) {
	t.Serial()
	// Test reflect.DeepEqual (negative)
	call := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   &ast.Ident{Name: "reflect"},
			Sel: &ast.Ident{Name: "DeepEqual"},
		},
	}
	assert.Equal(t, "NotEqual", determineNegativeAssertFunc(call))

	// Test parenthesized expression
	paren := &ast.ParenExpr{X: &ast.Ident{Name: "ok"}}
	assert.Equal(t, "False", determineNegativeAssertFunc(paren))
}

func TestDetermineAssertionWithInit(t *testing.T) {
	t.Serial()
	// Test init clause pattern: if err := X; err != nil
	ifStmt := &ast.IfStmt{
		Init: &ast.AssignStmt{
			Lhs: []ast.Expr{&ast.Ident{Name: "err"}},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{&ast.CallExpr{Fun: &ast.Ident{Name: "doSomething"}}},
		},
		Cond: &ast.BinaryExpr{
			X:  &ast.Ident{Name: "err"},
			Op: token.NEQ,
			Y:  &ast.Ident{Name: "nil"},
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ExprStmt{
					X: &ast.CallExpr{
						Fun: &ast.SelectorExpr{
							X:   &ast.Ident{Name: "t"},
							Sel: &ast.Ident{Name: "Fatal"},
						},
					},
				},
			},
		},
	}

	pkg, fn := determineAssertion(ifStmt)
	assert.Equal(t, "require", pkg)
	assert.Equal(t, "NoError", fn)
}
