package vet

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	_, err := vetSemantic("./...", NewEditor(false), nil)
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
	_, err := vetSemantic("./...", NewEditor(false), nil)
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
	// assertlint will add a stretchr/testify import; resolve it to the local
	// stub via a replace so the go mod tidy the fix triggers needs no network
	// (the per-package test timeout is tight).
	stub, err := filepath.Abs(filepath.Join("testdata", "src", "testifystub"))
	require.NoError(t, err)

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
	gomod := "module testmod\n\ngo 1.21\n\nrequire github.com/stretchr/testify v1.9.0\n\nreplace github.com/stretchr/testify => " + stub + "\n"
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0644)

	// Initialize git repo and commit the file (required by checkFileCommitted)
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	// Initialize git repo
	initGitRepo(t, dir)

	// With fix=true, it should apply fixes, run go mod tidy, and re-run vetSemantic
	changed, err := vetSemantic("./...", NewEditor(true), nil)
	assert.Nil(t, err)
	assert.True(t, changed)

	// Verify the fix was applied (should use assert.Equal)
	content, _ := os.ReadFile(filepath.Join(dir, "main_test.go"))
	assert.Contains(t, string(content), "assert.Equal")
}
