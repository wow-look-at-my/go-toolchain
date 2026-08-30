package vet

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	// 'i' is referenced in the body via _ = i, so it should not be replaced.
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
	// 'i' is used outside the range body; AST parsing does not require the code to compile.
	code = `package main

func main() {
	s := []int{1, 2, 3}
	for k := range s {
		println(s[0])
	}
	_ = k
}
`
	// Testing the core case: a range key unused inside the body gets replaced with _.
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

func TestCheckFileCommittedExec_Clean(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	os.WriteFile(src, []byte("package main\n"), 0644)
	initGitRepo(t, dir)

	err := checkFileCommittedExec(src)
	assert.NoError(t, err)
}

func TestCheckFileCommittedExec_Dirty(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	os.WriteFile(src, []byte("package main\n"), 0644)
	initGitRepo(t, dir)

	// Modify the file after commit
	os.WriteFile(src, []byte("package main\n\nfunc foo() {}\n"), 0644)

	err := checkFileCommittedExec(src)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "uncommitted changes")
}

func TestCheckFileCommittedExec_NotARepo(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	os.WriteFile(src, []byte("package main\n"), 0644)

	err := checkFileCommittedExec(src)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "git status failed")
}

func TestCheckFileCommittedGoGit_Clean(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	os.WriteFile(src, []byte("package main\n"), 0644)
	initGitRepo(t, dir)

	err := checkFileCommittedGoGit(src)
	assert.NoError(t, err)
}

func TestCheckFileCommittedGoGit_Dirty(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	os.WriteFile(src, []byte("package main\n"), 0644)
	initGitRepo(t, dir)

	os.WriteFile(src, []byte("package main\n\nfunc foo() {}\n"), 0644)

	err := checkFileCommittedGoGit(src)
	require.Error(t, err) // require, or the next line dereferences nil and the panic buries the real failure
	assert.Contains(t, err.Error(), "uncommitted changes")
}

// Pins support for repos whose index was written under feature.manyFiles, which on a recent git implies
// index.skipHash and writes an empty index trailer hash that go-git v5 cannot read ("invalid checksum";
// unreleased upstream fix https://github.com/go-git/go-git/pull/2181). This exercises checkFileCommittedByName's go-git-fails -> git-CLI
// fallback path, which is load-bearing here. index.skipHash is set explicitly too, so the trigger holds
// regardless of git version; on an older git the index stays normal and go-git succeeds directly.
func TestCheckFileCommittedByName_ManyFilesIndex(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(src, []byte("package main\n"), 0644))
	initGitRepoWithConfig(t, dir, [][2]string{
		{"feature.manyFiles", "true"},
		{"index.skipHash", "true"},
	})

	// Clean repo: the committed check must pass.
	assert.NoError(t, checkFileCommittedByName(src))

	// Dirty the file: the check must report uncommitted changes.
	require.NoError(t, os.WriteFile(src, []byte("package main\n\nfunc foo() {}\n"), 0644))
	err := checkFileCommittedByName(src)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "uncommitted changes")
}

func TestCheckFileCommittedFallback(t *testing.T) {
	// Happy path: both the go-git and git-CLI paths agree when git CLI works.
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	os.WriteFile(src, []byte("package main\n"), 0644)
	initGitRepo(t, dir)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, src, nil, 0)
	require.NoError(t, err)

	fixes := &ASTFixes{File: file, Fset: fset}
	err = checkFileCommitted(fixes)
	assert.NoError(t, err)
}
