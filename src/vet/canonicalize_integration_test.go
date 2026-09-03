package vet

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const canonicalizeDocFile = "doccomment_test.go"
const canonicalizeHoistFile = "hoist_test.go"

// The file carries an assertlint-fixable comparison, a top-level doc comment
// holding the POSIX single-quote escape below, and an already space-aligned struct.
const canonicalizeDocCode = "package main\n\n" +
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

// The fixture for the fixer writing code that does not compile. `if _, err :=
// f(); err != nil {}` declares into the if's own scope, so it is legal beside
// an `err` that exists; hoisted out verbatim it is not ("no new variables on
// left side of :="). TestShadowed's init must assign, TestFresh's must keep
// :=, and they share a file because only an existing name may convert.
const canonicalizeHoistCode = "package main\n\n" +
	"import \"testing\"\n\n" +
	"func mayFail() (int, error) { return 0, nil }\n\n" +
	"func TestShadowed(t *testing.T) {\n" +
	"\t_, err := mayFail()\n" +
	"\tif err != nil {\n" +
	"\t\tt.Fatal(\"setup\")\n" +
	"\t}\n" +
	"\tif _, err := mayFail(); err != nil {\n" +
	"\t\tt.Fatal(\"second\")\n" +
	"\t}\n" +
	"}\n\n" +
	"func TestFresh(t *testing.T) {\n" +
	"\tif _, err := mayFail(); err != nil {\n" +
	"\t\tt.Fatal(\"only\")\n" +
	"\t}\n" +
	"}\n"

// fixCanonicalizeFixture writes the module both canonicalize regressions share,
// runs the fixer over it, and returns the directory with the rewritten files by
// name. A fixer run spends a go mod tidy and a package load, so a module per
// regression repeats that for the same work; a real run fixes a package's files
// together anyway. Both files are package main and share no names.
func fixCanonicalizeFixture(t *testing.T) (string, map[string]string) {
	t.Helper()
	// assertlint adds a testify import; resolve it to the local stub so the triggered go mod tidy needs no network.
	stub, err := filepath.Abs(filepath.Join("testdata", "src", "testifystub"))
	require.NoError(t, err)

	dir := t.TempDir()
	sources := map[string]string{
		canonicalizeDocFile:   canonicalizeDocCode,
		canonicalizeHoistFile: canonicalizeHoistCode,
	}
	for name, code := range sources {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(code), 0644))
	}
	gomod := "module testmod\n\ngo 1.21\n\nrequire github.com/stretchr/testify v1.9.0\n\nreplace github.com/stretchr/testify => " + stub + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0644))

	oldWd, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer os.Chdir(oldWd)
	initGitRepo(t, dir)

	changed, err := vetSemantic("./...", NewEditor(true), nil)
	require.NoError(t, err)
	assert.True(t, changed)

	got := make(map[string]string, len(sources))
	for name := range sources {
		content, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		got[name] = string(content)
	}
	return dir, got
}

// TestVetSemanticFixCanonicalizes exercises the AST-fix write path in fix.go:
// when assertlint rewrites a file, fix.go reprints the whole AST through
// go/printer, which tab-aligns, applies gofmt's doc-comment smart-quote
// substitution, and hoists an if's init statement out of the if.
func TestVetSemanticFixCanonicalizes(t *testing.T) {
	dir, files := fixCanonicalizeFixture(t)

	// The canonicalize step must restore gofmt's "tabs to indent, spaces to
	// align" style and keep the author's literal apostrophe pair rather than
	// corrupting the comment into U+201D.
	t.Run("keeps doc comment quotes and alignment", func(t *testing.T) {
		got := files[canonicalizeDocFile]
		assert.Contains(t, got, "assert.Equal", "the assert fix was applied")
		assert.Contains(t, got, "'foo'\\''bar'", "the doc comment quoting survived the AST reprint")
		assert.NotContains(t, got, smartRight)
		assert.Contains(t, got, "\tBBB string\n", "the struct stays space-aligned after the reprint")
		assert.NotContains(t, got, "A\tint")
		assertPrintableASCII(t, got)
	})

	// See canonicalizeHoistCode for what each shape in the fixture pins.
	t.Run("hoists init legally", func(t *testing.T) {
		got := files[canonicalizeHoistFile]
		assert.Contains(t, got, "_, err = mayFail()", "a hoisted init whose names all exist must assign, not define")
		assert.Contains(t, got, "_, err := mayFail()", "a hoisted init with a new name must keep :=")
	})

	// A string check passes on broken output; only go/types catches a := error.
	build := exec.Command("go", "vet", "./...")
	build.Dir = dir
	out, err := build.CombinedOutput()
	require.NoError(t, err, "the rewritten files do not compile:\n%s\n--- %s ---\n%s\n--- %s ---\n%s",
		out, canonicalizeDocFile, files[canonicalizeDocFile], canonicalizeHoistFile, files[canonicalizeHoistFile])
}
