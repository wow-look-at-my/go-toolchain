package vet

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestRemoveImport(t *testing.T) {
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "test.go", `package main

import (
	"fmt"
	"strings"
)

func main() { fmt.Println("hi") }
`, parser.ParseComments)

	// Find the strings import
	var stringsImp *ast.ImportSpec
	for _, imp := range f.Imports {
		if strings.Contains(imp.Path.Value, "strings") {
			stringsImp = imp
			break
		}
	}
	require.NotNil(t, stringsImp)

	// Remove it
	removeImport(f, stringsImp)

	// Verify it's gone
	for _, imp := range f.Imports {
		assert.NotContains(t, imp.Path.Value, "strings")
	}
}

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

func TestGenerateReplacementFallback(t *testing.T) {
	// Test the fallback path with a complex expression
	dir := t.TempDir()
	testFile := filepath.Join(dir, "main_test.go")

	code := `package main

import "testing"

func TestFoo(t *testing.T) {
	x := []int{1, 2, 3}
	if len(x) > 0 {
		t.Error("should be empty")
	}
}
`
	os.WriteFile(testFile, []byte(code), 0644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	// Run to exercise the path
	_, err := vetSemantic("./...", false)
	assert.NotNil(t, err)
}

func TestRunWithGoMod(t *testing.T) {
	dir := t.TempDir()

	code := `package main

func main() {
	println("hello")
}
`
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(code), 0644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	_, err := Run(false)
	assert.Nil(t, err)
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

func TestVetSemanticWithDiagnostics(t *testing.T) {
	dir := t.TempDir()

	// Create test file that will trigger assertlint
	code := `package main

import "testing"

func TestFoo(t *testing.T) {
	err := error(nil)
	if err != nil {
		t.Error("oops")
	}
}
`
	os.WriteFile(filepath.Join(dir, "main_test.go"), []byte(code), 0644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	// Should find issues and return error with diagnostics
	_, err := vetSemantic("./...", false)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "vet found issues")
}

func TestIsRedundantCastChar(t *testing.T) {
	// Test char literal cases
	assert.True(t, isRedundantCast("rune", &ast.BasicLit{Kind: token.CHAR, Value: "'a'"}))
	assert.True(t, isRedundantCast("int32", &ast.BasicLit{Kind: token.CHAR, Value: "'a'"}))
	assert.False(t, isRedundantCast("byte", &ast.BasicLit{Kind: token.CHAR, Value: "'a'"}))
	assert.False(t, isRedundantCast("uint8", &ast.BasicLit{Kind: token.CHAR, Value: "'a'"}))
}

func TestVetSemanticWithFixRecursive(t *testing.T) {
	// Test that after applying fixes, vetSemantic re-runs and go mod tidy is called
	dir := t.TempDir()

	// Create test file that will trigger assertlint fixes
	code := `package main

import "testing"

func TestFoo(t *testing.T) {
	x := 5
	if x != 5 {
		t.Error("x should be 5")
	}
}
`
	os.WriteFile(filepath.Join(dir, "main_test.go"), []byte(code), 0644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)

	// Initialize git repo and commit the file (required by checkFileCommitted)
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	// Initialize git repo
	gitInit := exec.Command("git", "init")
	gitInit.Run()
	gitConfig1 := exec.Command("git", "config", "user.email", "test@test.com")
	gitConfig1.Run()
	gitConfig2 := exec.Command("git", "config", "user.name", "Test")
	gitConfig2.Run()
	gitAdd := exec.Command("git", "add", ".")
	gitAdd.Run()
	gitCommit := exec.Command("git", "commit", "-m", "initial")
	gitCommit.Run()

	// With fix=true, it should apply fixes, run go mod tidy, and re-run vetSemantic
	changed, err := vetSemantic("./...", true)
	assert.Nil(t, err)
	assert.True(t, changed)

	// Verify the fix was applied (should use assert.Equal)
	content, _ := os.ReadFile(filepath.Join(dir, "main_test.go"))
	assert.Contains(t, string(content), "assert.Equal")
}

func TestFixFileUnusedRangeVars_NoRangeStatements(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	os.WriteFile(src, []byte("package main\n\nfunc main() {\n\tx := 1\n\t_ = x\n}\n"), 0644)

	fixed, err := fixFileUnusedRangeVars(src)
	assert.Nil(t, err)
	assert.False(t, fixed)
}

func TestFixFileUnusedRangeVars_UnusedKey(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	code := `package main

func main() {
	s := []int{1, 2, 3}
	for i, v := range s {
		println(v)
		_ = i
	}
}
`
	// The 'i' is only used as _ = i, but the range key 'i' IS referenced
	// in the body, so it should NOT be replaced.
	os.WriteFile(src, []byte(code), 0644)

	fixed, err := fixFileUnusedRangeVars(src)
	assert.Nil(t, err)
	// 'i' is used in `_ = i`, so it counts as referenced
	assert.False(t, fixed)
}

func TestFixFileUnusedRangeVars_TrulyUnusedKey(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	code := `package main

func main() {
	s := []int{1, 2, 3}
	for i, v := range s {
		println(v)
	}
	_ = i
}
`
	// Note: 'i' is used OUTSIDE the range body (which means the code wouldn't
	// compile, but the AST analysis only looks inside the range body).
	// But this actually won't compile. Let me use a version that will parse.
	code = `package main

func main() {
	s := []int{1, 2, 3}
	for k := range s {
		println(s[0])
	}
	_ = k
}
`
	// Actually k is used outside the loop body so AST-wise it's not used inside.
	// But this code wouldn't compile either. Let me just test the core:
	// range key not used inside body → replaced with _
	code = `package main

func foo() {
	s := []string{"a", "b"}
	for idx, val := range s {
		println(val)
	}
	_ = idx
}
`
	// This won't compile cleanly but we're only parsing AST, not compiling.
	os.WriteFile(src, []byte(code), 0644)

	fixed, err := fixFileUnusedRangeVars(src)
	assert.Nil(t, err)
	assert.True(t, fixed)

	result, _ := os.ReadFile(src)
	assert.Contains(t, string(result), "for _, val := range")
}

func TestFixFileUnusedRangeVars_UnusedValue(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	code := `package main

func foo() {
	m := map[string]int{"a": 1}
	for key, val := range m {
		println(key)
	}
	_ = val
}
`
	os.WriteFile(src, []byte(code), 0644)

	fixed, err := fixFileUnusedRangeVars(src)
	assert.Nil(t, err)
	assert.True(t, fixed)

	result, _ := os.ReadFile(src)
	assert.Contains(t, string(result), "for key, _ := range")
}

func TestFixFileUnusedRangeVars_BothUsed(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	code := `package main

func foo() {
	s := []string{"a", "b"}
	for i, v := range s {
		println(i, v)
	}
}
`
	os.WriteFile(src, []byte(code), 0644)

	fixed, err := fixFileUnusedRangeVars(src)
	assert.Nil(t, err)
	assert.False(t, fixed)
}

func TestFixFileUnusedRangeVars_AlreadyUnderscore(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	code := `package main

func foo() {
	s := []string{"a", "b"}
	for _, v := range s {
		println(v)
	}
}
`
	os.WriteFile(src, []byte(code), 0644)

	fixed, err := fixFileUnusedRangeVars(src)
	assert.Nil(t, err)
	assert.False(t, fixed)
}

func TestFixFileUnusedRangeVars_ParseError(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "bad.go")
	os.WriteFile(src, []byte("this is not valid go {{{"), 0644)

	_, err := fixFileUnusedRangeVars(src)
	assert.NotNil(t, err)
}
