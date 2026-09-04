package vet

import (
	"go/ast"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImportNameAliased(t *testing.T) {
	t.Serial()
	t.Parallel() // Pure in-memory AST node, no process-wide state.
	imp := &ast.ImportSpec{
		Name: &ast.Ident{Name: "myfmt"},
		Path: &ast.BasicLit{Value: `"fmt"`},
	}
	assert.Equal(t, "myfmt", importName(imp))
}

func TestImportNameStdlib(t *testing.T) {
	t.Serial()
	t.Parallel() // See TestImportNameAliased.
	imp := &ast.ImportSpec{
		Path: &ast.BasicLit{Value: `"fmt"`},
	}
	assert.Equal(t, "fmt", importName(imp))
}

func TestImportNameFallback(t *testing.T) {
	t.Serial()
	t.Parallel() // See TestImportNameAliased.
	// Non-existent package falls back to filepath.Base
	imp := &ast.ImportSpec{
		Path: &ast.BasicLit{Value: `"example.invalid/nonexistent/mypkg"`},
	}
	assert.Equal(t, "mypkg", importName(imp))
}

func TestImportNameDotImport(t *testing.T) {
	t.Serial()
	t.Parallel() // See TestImportNameAliased.
	imp := &ast.ImportSpec{
		Name: &ast.Ident{Name: "."},
		Path: &ast.BasicLit{Value: `"fmt"`},
	}
	assert.Equal(t, ".", importName(imp))
}

func TestImportNameBlankImport(t *testing.T) {
	t.Serial()
	t.Parallel() // See TestImportNameAliased.
	imp := &ast.ImportSpec{
		Name: &ast.Ident{Name: "_"},
		Path: &ast.BasicLit{Value: `"fmt"`},
	}
	assert.Equal(t, "_", importName(imp))
}

func TestFixUnusedRangeVarsNoFiles(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	t.Chdir(dir)

	fixed, err := FixUnusedRangeVars("./...")
	require.NoError(t, err)
	assert.Empty(t, fixed)
}

func TestFixUnusedRangeVarsNothingToFix(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	t.Chdir(dir)

	// Create a Go file where range vars are used
	code := `package main

func main() {
	for k, v := range map[string]int{"a": 1} {
		_ = k
		_ = v
	}
}
`
	os.WriteFile("main.go", []byte(code), 0644)

	fixed, err := FixUnusedRangeVars("./...")
	require.NoError(t, err)
	assert.Empty(t, fixed)
}

func TestFixUnusedRangeVarsFixesUnused(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	t.Chdir(dir)

	// Create a Go file with unused range variable
	code := `package main

func main() {
	for k, v := range map[string]int{"a": 1} {
		_ = v
	}
}
`
	os.WriteFile("main.go", []byte(code), 0644)

	fixed, err := FixUnusedRangeVars("./...")
	require.NoError(t, err)
	assert.Equal(t, []string{"main.go"}, fixed)

	// Verify the file was modified
	data, err := os.ReadFile("main.go")
	require.NoError(t, err)
	assert.Contains(t, string(data), "_, v := range")
}

func TestFixUnusedRangeVarsGlobPattern(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	t.Chdir(dir)

	code := `package main

func main() {
	for k := range map[string]int{"a": 1} {
		_ = k
	}
}
`
	os.WriteFile("main.go", []byte(code), 0644)

	fixed, err := FixUnusedRangeVars("*.go")
	require.NoError(t, err)
	assert.Empty(t, fixed) // k is used
}

func TestFixUnusedRangeVarsSkipsVendor(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	t.Chdir(dir)

	// Create a file in vendor/
	os.MkdirAll(filepath.Join(dir, "vendor", "pkg"), 0755)
	code := `package pkg

func Foo() {
	for k, v := range map[string]int{"a": 1} {
		_ = v
	}
}
`
	os.WriteFile(filepath.Join(dir, "vendor", "pkg", "foo.go"), []byte(code), 0644)

	fixed, err := FixUnusedRangeVars("./...")
	require.NoError(t, err)
	assert.Empty(t, fixed) // vendor should be skipped
}

func TestRemoveImportFromAST(t *testing.T) {
	t.Serial()
	t.Parallel() // Pure in-memory AST mutation, no process-wide state.
	f := &ast.File{
		Decls: []ast.Decl{
			&ast.GenDecl{
				Tok: 75, // token.IMPORT
				Specs: []ast.Spec{
					&ast.ImportSpec{Path: &ast.BasicLit{Value: `"fmt"`}},
					&ast.ImportSpec{Path: &ast.BasicLit{Value: `"os"`}},
				},
			},
		},
	}
	f.Imports = []*ast.ImportSpec{
		f.Decls[0].(*ast.GenDecl).Specs[0].(*ast.ImportSpec),
		f.Decls[0].(*ast.GenDecl).Specs[1].(*ast.ImportSpec),
	}

	// Remove "fmt"
	removeImport(f, f.Imports[0])
	assert.Equal(t, 1, len(f.Decls[0].(*ast.GenDecl).Specs))
	assert.Equal(t, 1, len(f.Imports))
	assert.Equal(t, `"os"`, f.Imports[0].Path.Value)
}
