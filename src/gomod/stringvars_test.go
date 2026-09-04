package gomod

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeGoFile puts src in dir under name.
func writeGoFile(t *testing.T, dir, name, src string) {
	t.Helper()
	require.Nil(t, os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644))
}

func TestPackageStringVarsReportsWhatTheLinkerCanSet(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "main.go", `package main

type label string

var typed string
var literal = "x"
var grouped, alsoGrouped string
var (
	inBlock  = "y"
	computed = compute()
	numeric  int
	named    label
	mismatch = split()
)

func compute() string { return "" }
func split() (string, string) { return "", "" }

func main() {
	var local string
	_ = local
}
`)

	got := PackageStringVars(dir)
	assert.True(t, got.ContainsAll("typed", "literal", "grouped", "alsoGrouped", "inBlock"))
	// -X fails the LINK for a variable of another type.
	assert.False(t, got.ContainsAny("computed", "numeric", "named", "mismatch", "local", "compute"))
}

func TestPackageStringVarsSkipsTestFilesAndUnreadableDirectories(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "main.go", "package main\n\nvar shipped = \"a\"\n")
	writeGoFile(t, dir, "main_test.go", "package main\n\nvar fixture = \"b\"\n")
	writeGoFile(t, dir, "notes.txt", "var decoy = \"c\"")

	got := PackageStringVars(dir)
	assert.True(t, got.Contains("shipped"))
	assert.False(t, got.ContainsAny("fixture", "decoy"),
		"a test file is not linked into the binary, and a text file is not source")

	assert.Equal(t, 0, PackageStringVars(filepath.Join(dir, "absent")).Len())
}

func TestPackageStringVarsReadsThePartialASTOfAFileThatDoesNotParse(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "broken.go", "package main\n\nvar early = \"a\"\n\nfunc (\n")

	assert.True(t, PackageStringVars(dir).Contains("early"),
		"a build in progress must still resolve the names it can read")
}
