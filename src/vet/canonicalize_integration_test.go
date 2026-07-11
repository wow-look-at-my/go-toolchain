package vet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVetSemanticFixKeepsDocCommentQuotesAndAlignment exercises the AST-fix
// write path in fix.go: when assertlint rewrites a file, fix.go reprints the
// whole AST through go/printer, which tab-aligns and applies gofmt's
// doc-comment smart-quote substitution. The canonicalize step must restore
// gofmt's "tabs to indent, spaces to align" style and keep the author's literal
// apostrophe-pair rather than corrupting the comment into U+201D.
func TestVetSemanticFixKeepsDocCommentQuotesAndAlignment(t *testing.T) {
	// assertlint adds a stretchr/testify import; resolve it to the local stub via
	// a replace so the go mod tidy the fix triggers needs no network.
	stub, err := filepath.Abs(filepath.Join("testdata", "src", "testifystub"))
	require.NoError(t, err)

	dir := t.TempDir()

	// The file has (1) an assertlint-fixable comparison, (2) a top-level doc
	// comment containing the POSIX 'foo'\''bar' escape, and (3) an already
	// space-aligned struct.
	code := "package main\n\n" +
		"import \"testing\"\n\n" +
		"// Doc: foo'bar becomes 'foo'\\''bar' in a POSIX shell.\n" +
		"type T struct {\n" +
		"\tA   int\n" +
		"\tBBB string\n" +
		"}\n\n" +
		"func TestFoo(t *testing.T) {\n" +
		"\tx := 5\n" +
		"\tif x != 5 {\n" +
		"\t\tt.Error(\"x should be 5\")\n" +
		"\t}\n" +
		"}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main_test.go"), []byte(code), 0644))
	gomod := "module testmod\n\ngo 1.21\n\nrequire github.com/stretchr/testify v1.9.0\n\nreplace github.com/stretchr/testify => " + stub + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0644))

	oldWd, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer os.Chdir(oldWd)
	initGitRepo(t, dir)

	changed, err := vetSemantic("./...", NewEditor(true), nil)
	require.NoError(t, err)
	assert.True(t, changed)

	content, err := os.ReadFile(filepath.Join(dir, "main_test.go"))
	require.NoError(t, err)
	got := string(content)

	// The assert fix was applied.
	assert.Contains(t, got, "assert.Equal")
	// The doc comment '' survived the AST reprint (was NOT turned into U+201D).
	assert.Contains(t, got, "'foo'\\''bar'")
	assert.NotContains(t, got, smartRight)
	// The struct stays space-aligned, not tab-aligned, after the reprint.
	assert.Contains(t, got, "\tBBB string\n")
	assert.NotContains(t, got, "A\tint")
	assertPrintableASCII(t, got)
}
