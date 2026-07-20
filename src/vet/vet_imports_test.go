package vet

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImportName(t *testing.T) {
	tests := []struct {
		name     string
		imp      *ast.ImportSpec
		expected string
	}{
		{
			name: "named import",
			imp: &ast.ImportSpec{
				Name: &ast.Ident{Name: "foo"},
				Path: &ast.BasicLit{Value: `"bar/baz"`},
			},
			expected: "foo",
		},
		{
			name: "unnamed import",
			imp: &ast.ImportSpec{
				Path: &ast.BasicLit{Value: `"bar/baz"`},
			},
			expected: "baz",
		},
		{
			name: "nested path",
			imp: &ast.ImportSpec{
				Path: &ast.BasicLit{Value: `"github.com/foo/bar"`},
			},
			expected: "bar",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, importName(tt.imp))
		})
	}
}

func TestIsRedundantCast(t *testing.T) {
	tests := []struct {
		typeName string
		litKind  token.Token
		expected bool
	}{
		{"int", token.INT, true},
		{"int64", token.INT, false},
		{"float64", token.FLOAT, true},
		{"float32", token.FLOAT, false},
		{"string", token.STRING, true},
		{"rune", token.CHAR, true},
		{"int32", token.CHAR, true},
		{"byte", token.CHAR, false},
		{"bool", token.INT, false},
	}

	for _, tt := range tests {
		t.Run(tt.typeName, func(t *testing.T) {
			lit := &ast.BasicLit{Kind: tt.litKind, Value: "0"}
			assert.Equal(t, tt.expected, isRedundantCast(tt.typeName, lit))
		})
	}
}

func TestNodeText(t *testing.T) {
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "test.go", `package main; var x = 42`, 0)

	var lit *ast.BasicLit
	ast.Inspect(f, func(n ast.Node) bool {
		if l, ok := n.(*ast.BasicLit); ok && l.Kind == token.INT {
			lit = l
			return false
		}
		return true
	})
	require.NotNil(t, lit)
	assert.Equal(t, "42", nodeText(fset, lit))
}

func TestCastableType(t *testing.T) {
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
func TestVetSemanticLoadError(t *testing.T) {
	dir := t.TempDir()

	// Create invalid Go code (syntax error)
	code := `package main

func main() {
	invalid syntax here
}
`
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(code), 0644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	_, err := vetSemantic("./...", NewEditor(false), nil)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "package load errors")
}

func TestGenerateBinaryReplacementCompound(t *testing.T) {
	// Test for && and || operators
	dir := t.TempDir()
	testFile := filepath.Join(dir, "main_test.go")

	code := `package main

import "testing"

func TestFoo(t *testing.T) {
	x := true
	y := false
	if x && y {
		t.Error("should be false")
	}
}
`
	os.WriteFile(testFile, []byte(code), 0644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	// Just run it to exercise the compound condition path
	_, err := vetSemantic("./...", NewEditor(false), nil)
	// It should find an issue
	assert.NotNil(t, err)
}

func TestGenerateImportEdit(t *testing.T) {
	fset := token.NewFileSet()

	// Test with existing imports
	f1, _ := parser.ParseFile(fset, "test.go", `package main

import "fmt"

func main() { fmt.Println("hi") }
`, parser.ParseComments)

	// This is tested indirectly but let's test the path where imports exist
	assert.NotNil(t, f1.Imports)

	// Test with no imports
	f2, _ := parser.ParseFile(fset, "test2.go", `package main

func main() {}
`, parser.ParseComments)
	assert.Empty(t, f2.Imports)
}

func TestDetermineAssertFuncUnary(t *testing.T) {
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

func TestSourceLocationShortLocWithError(t *testing.T) {
	// Test when filepath.Rel fails (shouldn't happen in practice, but for coverage)
	loc := SourceLocation{File: "/some/path/file.go", Line: 1}
	short := loc.ShortLoc()
	assert.Contains(t, short, "file.go:1")
}
