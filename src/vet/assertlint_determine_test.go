package vet

import (
	"go/ast"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetermineAssertionNotInit(t *testing.T) {
	// Test with init that's not an AssignStmt
	ifStmt := &ast.IfStmt{
		Init: &ast.ExprStmt{X: &ast.Ident{Name: "x"}},
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
							Sel: &ast.Ident{Name: "Error"},
						},
					},
				},
			},
		},
	}

	pkg, fn := determineAssertion(ifStmt)
	assert.Equal(t, "assert", pkg)
	assert.Equal(t, "Nil", fn)
}

func TestDetermineAssertionInitMultipleLhs(t *testing.T) {
	// Test with init that has multiple LHS
	ifStmt := &ast.IfStmt{
		Init: &ast.AssignStmt{
			Lhs: []ast.Expr{&ast.Ident{Name: "x"}, &ast.Ident{Name: "err"}},
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
							Sel: &ast.Ident{Name: "Error"},
						},
					},
				},
			},
		},
	}

	pkg, fn := determineAssertion(ifStmt)
	assert.Equal(t, "assert", pkg)
	// Not NoError because init has multiple LHS
	assert.Equal(t, "Nil", fn)
}

func TestDetermineAssertionCondNotBinary(t *testing.T) {
	// Test with init where cond is not binary
	ifStmt := &ast.IfStmt{
		Init: &ast.AssignStmt{
			Lhs: []ast.Expr{&ast.Ident{Name: "ok"}},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{&ast.CallExpr{Fun: &ast.Ident{Name: "check"}}},
		},
		Cond: &ast.Ident{Name: "ok"},
		Body: &ast.BlockStmt{
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
		},
	}

	pkg, fn := determineAssertion(ifStmt)
	assert.Equal(t, "assert", pkg)
	assert.Equal(t, "False", fn)
}

func TestDeterminePositiveAssertFuncNEQ(t *testing.T) {
	// Test NotEqual
	cond := &ast.BinaryExpr{
		X:  &ast.Ident{Name: "x"},
		Op: token.NEQ,
		Y:  &ast.Ident{Name: "y"},
	}
	assert.Equal(t, "NotEqual", determinePositiveAssertFunc(cond))
}

func TestDetermineNegativeAssertFuncComparisons(t *testing.T) {
	tests := []struct {
		op       token.Token
		expected string
	}{
		{token.LEQ, "Greater"},
		{token.GEQ, "Less"},
	}

	for _, tt := range tests {
		cond := &ast.BinaryExpr{
			X:  &ast.Ident{Name: "x"},
			Op: tt.op,
			Y:  &ast.Ident{Name: "y"},
		}
		assert.Equal(t, tt.expected, determineNegativeAssertFunc(cond))
	}
}

func TestDetermineAssertionInitNotDefine(t *testing.T) {
	// Test with init that uses = instead of :=
	ifStmt := &ast.IfStmt{
		Init: &ast.AssignStmt{
			Lhs: []ast.Expr{&ast.Ident{Name: "err"}},
			Tok: token.ASSIGN, // = not :=
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
							Sel: &ast.Ident{Name: "Error"},
						},
					},
				},
			},
		},
	}

	pkg, fn := determineAssertion(ifStmt)
	assert.Equal(t, "assert", pkg)
	assert.Equal(t, "Nil", fn) // Not NoError because not :=
}

func TestDetermineAssertionCondVarMismatch(t *testing.T) {
	// Test where condition variable doesn't match init variable
	ifStmt := &ast.IfStmt{
		Init: &ast.AssignStmt{
			Lhs: []ast.Expr{&ast.Ident{Name: "err"}},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{&ast.CallExpr{Fun: &ast.Ident{Name: "doSomething"}}},
		},
		Cond: &ast.BinaryExpr{
			X:  &ast.Ident{Name: "otherErr"}, // Different variable
			Op: token.NEQ,
			Y:  &ast.Ident{Name: "nil"},
		},
		Body: &ast.BlockStmt{
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
		},
	}

	pkg, fn := determineAssertion(ifStmt)
	assert.Equal(t, "assert", pkg)
	assert.Equal(t, "Nil", fn) // Not NoError
}

func TestDetermineAssertionCondNotNil(t *testing.T) {
	// Test where condition Y is not nil
	ifStmt := &ast.IfStmt{
		Init: &ast.AssignStmt{
			Lhs: []ast.Expr{&ast.Ident{Name: "err"}},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{&ast.CallExpr{Fun: &ast.Ident{Name: "doSomething"}}},
		},
		Cond: &ast.BinaryExpr{
			X:  &ast.Ident{Name: "err"},
			Op: token.NEQ,
			Y:  &ast.Ident{Name: "someValue"}, // Not nil
		},
		Body: &ast.BlockStmt{
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
		},
	}

	pkg, fn := determineAssertion(ifStmt)
	assert.Equal(t, "assert", pkg)
	assert.Equal(t, "Equal", fn) // Not NoError
}

func TestDetermineAssertionCondEQL(t *testing.T) {
	// Test where condition is == instead of !=
	ifStmt := &ast.IfStmt{
		Init: &ast.AssignStmt{
			Lhs: []ast.Expr{&ast.Ident{Name: "err"}},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{&ast.CallExpr{Fun: &ast.Ident{Name: "doSomething"}}},
		},
		Cond: &ast.BinaryExpr{
			X:  &ast.Ident{Name: "err"},
			Op: token.EQL, // == instead of !=
			Y:  &ast.Ident{Name: "nil"},
		},
		Body: &ast.BlockStmt{
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
		},
	}

	pkg, fn := determineAssertion(ifStmt)
	assert.Equal(t, "assert", pkg)
	assert.Equal(t, "NotNil", fn) // Not NoError
}

func TestDetermineAssertionCondXNotIdent(t *testing.T) {
	// Test where condition X is not an Ident
	ifStmt := &ast.IfStmt{
		Init: &ast.AssignStmt{
			Lhs: []ast.Expr{&ast.Ident{Name: "err"}},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{&ast.CallExpr{Fun: &ast.Ident{Name: "doSomething"}}},
		},
		Cond: &ast.BinaryExpr{
			X:  &ast.CallExpr{Fun: &ast.Ident{Name: "getErr"}}, // Not an Ident
			Op: token.NEQ,
			Y:  &ast.Ident{Name: "nil"},
		},
		Body: &ast.BlockStmt{
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
		},
	}

	pkg, fn := determineAssertion(ifStmt)
	assert.Equal(t, "assert", pkg)
	assert.Equal(t, "Nil", fn) // Fallback
}
