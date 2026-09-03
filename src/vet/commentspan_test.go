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
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"golang.org/x/tools/go/analysis"
)

// fixtureComment returns a comment with exactly n non-blank lines and total
// non-whitespace chars, for exact boundary tests. Every line needs at least
// its "//" marker, so chars must cover a marker per line.
func fixtureComment(t *testing.T, lines, chars int) string {
	t.Helper()
	require.GreaterOrEqual(t, chars, 2*lines, "need at least // per line")
	rows := make([]string, lines)
	for i := range rows {
		rows[i] = "//"
	}
	rows[lines-1] += strings.Repeat("x", chars-2*lines)
	return strings.Join(rows, "\n")
}

// runCommentSpanOn writes src to a real file (checkCommentSpanFile reads the
// file back from disk to measure it) and runs the analyzer over it.
func runCommentSpanOn(t *testing.T, src string) []logger.Warning {
	t.Helper()
	t.Serial() // The dedup record and the counters belong to the run.
	resetCommentSpanWarnings()
	logger.ResetWarnCount()
	t.Cleanup(logger.ResetWarnCount)

	path := filepath.Join(t.TempDir(), "fixture.go")
	require.NoError(t, os.WriteFile(path, []byte(src), 0o644))

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	require.NoError(t, err)

	pass := &analysis.Pass{
		Fset:  fset,
		Files: []*ast.File{file},
		Report: func(analysis.Diagnostic) {
			t.Fatal("commentspan warns; it never fails a build")
		},
	}
	_, err = runCommentSpan(pass)
	require.NoError(t, err)
	return logger.EmittedWarnings()
}

func TestCommentSpanMeasureIgnoresWhitespace(t *testing.T) {
	lines, chars := commentSpanMeasure("  // a b \n\n   \n// c")
	assert.Equal(t, 2, lines, "the whitespace-only line is not a line")
	assert.Equal(t, 7, chars, "spaces inside a line and around // do not count")
}

// TestCommentSpanFailsOnMoreLines runs the shape the analyzer exists for: a
// multi-line doc over a single-line const. Only the line clamp is broken.
func TestCommentSpanFailsOnMoreLines(t *testing.T) {
	const src = `package p

// line one
// line two
const x = 1
`
	warnings := runCommentSpanOn(t, src)
	require.Len(t, warnings, 1)
	msg := warnings[0].Message
	assert.Contains(t, msg, "2 comment lines > 1 code lines = 1 lines too long")
	assert.NotContains(t, msg, "comment chars >")
}

// TestCommentSpanFailsOnMoreCharsOnly is a single-line doc long enough to
// break the char clamp while its single line still fits the line clamp.
func TestCommentSpanFailsOnMoreCharsOnly(t *testing.T) {
	src := "package p\n\n" + fixtureComment(t, 1, 160) + "\nconst x = 1\n"
	warnings := runCommentSpanOn(t, src)
	require.Len(t, warnings, 1)
	msg := warnings[0].Message
	assert.Contains(t, msg, "160 comment chars > 120 code chars = 40 chars too long")
	assert.NotContains(t, msg, "comment lines >")
}

// TestCommentSpanPassesAtTheCharFloor verifies a comment gets the full
// char floor even over a const with far fewer chars of its own.
func TestCommentSpanPassesAtTheCharFloor(t *testing.T) {
	src := "package p\n\n" + fixtureComment(t, 1, 120) + "\nconst x = 1\n"
	assert.Empty(t, runCommentSpanOn(t, src))
}

// TestCommentSpanFailsOneCharPastTheFloor verifies the floor is exact, not
// approximate.
func TestCommentSpanFailsOneCharPastTheFloor(t *testing.T) {
	src := "package p\n\n" + fixtureComment(t, 1, 121) + "\nconst x = 1\n"
	warnings := runCommentSpanOn(t, src)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0].Message, "121 comment chars > 120 code chars = 1 chars too long")
}

// TestCommentSpanPassesAtTheBoundary is a doc exactly as big as the function
// it documents, on both dimensions. The clamp is not-more-than, so equal
// passes.
func TestCommentSpanPassesAtTheBoundary(t *testing.T) {
	const fn = "func f() {\n\treturn\n}"
	tLines, tChars := commentSpanMeasure(fn)
	src := "package p\n\n" + fixtureComment(t, tLines, tChars) + "\n" + fn + "\n"
	assert.Empty(t, runCommentSpanOn(t, src))
}

// TestCommentSpanExemptsDirectivesAndPackageDoc verifies a //go:build line, a
// //go:generate line, and the package doc are never measured, however big.
func TestCommentSpanExemptsDirectivesAndPackageDoc(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"go:build", "//go:build linux\n\npackage p\n\nconst x = 1\n"},
		{"go:generate", "package p\n\n//go:generate stringer -type=T\ntype T int\n"},
		{"package doc", strings.Repeat("// filler\n", 20) + "package p\n\nconst x = 1\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Empty(t, runCommentSpanOn(t, c.src))
		})
	}
}

// TestCommentSpanAttachesToTheRightNode verifies a trailing comment, a struct
// field's doc, and a comment inside a case clause each attach to the small
// node beside them, not to something so big the check never fires.
func TestCommentSpanAttachesToTheRightNode(t *testing.T) {
	filler := strings.Repeat("x", 130)
	cases := []struct {
		name string
		src  string
	}{
		{
			"trailing comment",
			"package p\n\nfunc f() {\n\tx := 1 //" + filler + "\n\t_ = x\n}\n",
		},
		{
			"struct field",
			"package p\n\ntype T struct {\n\t// " + filler + "\n\tName string\n}\n",
		},
		{
			"case clause",
			"package p\n\nfunc f(v int) {\n\tswitch v {\n\tcase 1:\n\t\t// " + filler + "\n\t\t_ = v\n\t}\n}\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			warnings := runCommentSpanOn(t, c.src)
			require.Len(t, warnings, 1, "the comment must be measured against its own small node")
		})
	}
}

// TestCommentSpanWarnsOncePerSite mirrors writeruns' and mapset's own dedup
// test: go/packages loads a package several ways, so a repeated run over
// the same pass must not spend the budget again on a site already warned.
func TestCommentSpanWarnsOncePerSite(t *testing.T) {
	t.Serial()
	src := "package p\n\n" + fixtureComment(t, 1, 160) + "\nconst x = 1\n"
	resetCommentSpanWarnings()
	logger.ResetWarnCount()
	t.Cleanup(logger.ResetWarnCount)

	path := filepath.Join(t.TempDir(), "fixture.go")
	require.NoError(t, os.WriteFile(path, []byte(src), 0o644))
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	require.NoError(t, err)
	pass := &analysis.Pass{
		Fset:  fset,
		Files: []*ast.File{file},
		Report: func(analysis.Diagnostic) {
			t.Fatal("commentspan warns; it never fails a build")
		},
	}

	for range 4 {
		_, err = runCommentSpan(pass)
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, logger.TotalWarnCount())

	// A later vet run reports the site again.
	resetCommentSpanWarnings()
	_, err = runCommentSpan(pass)
	require.NoError(t, err)
	require.EqualValues(t, 2, logger.TotalWarnCount())
}
