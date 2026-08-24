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
	// Resolve testify to the local stub so the fix's go mod tidy needs no network.
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

// TestVetSemanticCastAddsMissingImport is a regression test for the testify
// cast fixer wedging a tree: converting a bare permission literal compared
// against an os.FileMode operand inserts fs.FileMode(...) — os.FileMode is an
// alias for io/fs.FileMode, so the conversion is spelled with the io/fs
// package even when the file only imports os. The fixer must add the io/fs
// import alongside the cast; without it the rewritten file fails to load
// (undefined: fs) and every later vet run — including the fix's own verify
// re-run — dies at the type-check with a package load error before any fixer
// runs, so the tree can never converge.
func TestVetSemanticCastAddsMissingImport(t *testing.T) {
	// Resolve upstream testify to the local stub so the fixture type-checks
	// hermetically (same pattern as TestVetSemanticWithFixRecursive).
	stub, err := filepath.Abs(filepath.Join("testdata", "src", "testifystub"))
	require.NoError(t, err)

	dir := t.TempDir()

	// info.Mode() is declared in io/fs (fs.FileInfo's method), so the operand's
	// type is the origin io/fs.FileMode — not the os.FileMode alias — and the
	// conversion must be spelled through the io/fs package.
	code := `package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMode(t *testing.T) {
	info, _ := os.Stat(".")
	assert.NotEqual(t, 0, info.Mode()&os.ModeSymlink)
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main_test.go"), []byte(code), 0644))
	// go 1.24 matches the stub module's go directive: the fixture imports
	// testify from the start, so the strict (compiled/test) package load already
	// needs the stub's module graph — an older go directive here fails the load
	// with "updates to go.mod needed" before the analyzer ever runs.
	gomod := "module testmod\n\ngo 1.24\n\nrequire github.com/stretchr/testify v1.9.0\n\nreplace github.com/stretchr/testify => " + stub + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0644))

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	initGitRepo(t, dir)

	// First run rewrites the literal; its internal verify re-run must load the
	// rewritten file cleanly (this used to fail with "undefined: fs").
	changed, err := vetSemantic("./...", NewEditor(true), nil)
	require.NoError(t, err)
	assert.True(t, changed)

	content, err := os.ReadFile(filepath.Join(dir, "main_test.go"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "assert.NotEqual(t, fs.FileMode(0), info.Mode()&os.ModeSymlink)")
	assert.Contains(t, string(content), `"io/fs"`)

	// Second run: the rewritten tree is canonical — it must load and no-op.
	changed, err = vetSemantic("./...", NewEditor(true), nil)
	require.NoError(t, err)
	assert.False(t, changed)
}
