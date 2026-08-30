package vet

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"golang.org/x/tools/go/analysis"
)

// TestCommentNumbersReadsACount pins what the check calls a number: digits
// standing alone, digits wearing an ordinal suffix, and a number spelled out.
func TestCommentNumbersReadsACount(t *testing.T) {
	for _, c := range []struct {
		name string
		text string
		want string
	}{
		{"a digit", "// there are 3 cases", "3"},
		{"a budget", "// fails past the 15-warning budget", "15"},
		{"a decimal", "// scales by 1.5x", "1"},
		{"an ordinal in digits", "// the 2nd pass rewrites", "2"},
		{"a cardinal word", "// there are three cases", "three"},
		{"a hyphenated word", "// twenty-five files", "twenty"},
		{"a capital word", "// Once the file lands", "Once"},
		{"a word ending a sentence", "// the analyzer runs once.", "once"},
		{"an ordinal word", "// the first pass", "first"},
		{"a ranked name", "// keeps the top-3 rows", "3"},
		{"a spaced encoding", "// decodes UTF-8", "8"},
		{"a version", "// present at otel v1.44.0", "44"},
		{"a block comment", "/* holds two entries */", "two"},
		{"a status code with no prefix", "// the proxy answers 403 here", "403"},
		{"a count after the prefix", "// the HTTP 4 retries", "4"},
		{"a count after a section citation", "// §7.3 covers 4 shapes", "4"},
		{"a count beside an amount", "// $1 is the boundary, and 4 dp under it", "4"},
		{"a spaced currency sign", "// costs $ 5 per call", "5"},
	} {
		t.Run(c.name, func(t *testing.T) {
			found := commentNumbers(c.text)
			require.Len(t, found, 1)
			assert.Equal(t, c.want, found[0].text)
			assert.Equal(t, c.want, c.text[found[0].offset:found[0].offset+len(c.want)])
		})
	}
}

// TestCommentNumbersLeavesNamesAlone pins the other side: a digit that belongs
// to a technical name, and a number word that belongs to an identifier, are
// what a comment has to be able to say.
func TestCommentNumbersLeavesNamesAlone(t *testing.T) {
	for _, c := range []struct {
		name string
		text string
	}{
		{"a hash", "// the sha256 of the payload"},
		{"an architecture", "// amd64 only"},
		{"a duration", "// waits 10ms for the ack"},
		{"a percentile", "// the p95 latency"},
		{"a major version suffix", "// example.com/mod/v2 resolves"},
		{"a url", "// see https://example.com/pull/419"},
		{"a qualified identifier", "// sync.Once guards the probe"},
		{"a package path", "// net/http serves it"},
		{"a camel case name", "// oneShot runs the probe"},
		{"a longer word", "// someone reads this later"},
		{"a format verb", "// %d formats the count"},
		{"an http status", "// the proxy answers HTTP 403 for a private repo"},
		{"a lowercase http status", "// returns http 200 with an empty body"},
		{"an http status ending a sentence", "// the fetch fails with HTTP 404."},
		{"a section citation", "// the encoding of §7.3 governs this"},
		{"a spaced section citation", "// see § 4 of the specification"},
		{"a dollar amount", "// $1.43 would be the lie the report exists to stop"},
		{"a bare dollar", "// renders $0 rather than an empty cell"},
		{"a dollar boundary", "// under $1 the cents matter"},
		{"prose with no number", "// the analyzer reports what it finds"},
	} {
		t.Run(c.name, func(t *testing.T) {
			assert.Empty(t, commentNumbers(c.text))
		})
	}
}

// TestCommentNumbersWarnsInEveryModule pins the severity: a stale count is
// prose, so it never fails a build by itself, in org code or anywhere else.
// The warnings budget is what turns a repo full of them red.
func TestCommentNumbersWarnsInEveryModule(t *testing.T) {
	const src = `package main

// holds three entries
var m = map[string]int{}

func main() { _ = m }
`
	for _, c := range []struct {
		name   string
		module *analysis.Module
	}{
		{"org module", &analysis.Module{Path: "github.com/wow-look-at-my/go-toolchain"}},
		{"PazerOP module", &analysis.Module{Path: "github.com/PazerOP/tool"}},
		{"third-party module", &analysis.Module{Path: "example.com/consumer"}},
		{"unknown module", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			// The helper fails the test on a diagnostic, so this counts warnings.
			assert.Len(t, runCommentNumbersOnSource(t, src, c.module), 1)
		})
	}
}

// TestCommentNumbersSpendsAWarningPerLine pins the file:line dedup the budget
// depends on: a line naming several numbers is a sentence to rewrite.
func TestCommentNumbersSpendsAWarningPerLine(t *testing.T) {
	const src = `package main

// three of the five slots stay empty
func main() {}
`
	require.Len(t, runCommentNumbersOnSource(t, src, nil), 1)
}

// TestCommentNumbersNamesTheRemedy pins that a finding quotes the number and
// says what to do instead, since the author has to rewrite the sentence.
func TestCommentNumbersNamesTheRemedy(t *testing.T) {
	warnings := runCommentNumbersOnSource(t, "package main\n\n// runs the probe twice\nfunc main() {}\n", nil)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0].Message, `"twice" is a number in a comment`)
	assert.Contains(t, warnings[0].Message, commentNumbersRemedy)
	assert.Contains(t, warnings[0].Message, "slug", "a citation of a numbered section needs the slug remedy, not the rewrite one")
}

// TestCommentNumbersSkipsMachineText pins the comments that are not prose: a
// directive is read by a tool, and a generated file is written by a program.
func TestCommentNumbersSkipsMachineText(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
	}{
		{"a build tag", "//go:build linux && amd64\n\npackage main\n\nfunc main() {}\n"},
		{"a generate directive", "package main\n\n//go:generate stringer -type=Kind -linecomment=3\nfunc main() {}\n"},
		{"a generated file", "// Code generated by stringer. DO NOT EDIT.\n\npackage main\n\n// holds three entries\nfunc main() {}\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			assert.Empty(t, runCommentNumbersOnSource(t, c.src, nil))
		})
	}
}

// runCommentNumbersOnSource parses src and returns the warnings the analyzer
// emitted. A diagnostic fails the test: this check never fails a build itself.
// No type information is needed, since the check reads comments.
func runCommentNumbersOnSource(t *testing.T, src string, module *analysis.Module) []logger.Warning {
	t.Helper()
	resetCommentNumbersWarnings()
	logger.ResetWarnCount()
	t.Cleanup(logger.ResetWarnCount)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "/consumer/main.go", src, parser.ParseComments)
	require.NoError(t, err)

	pass := &analysis.Pass{
		Analyzer: CommentNumbersAnalyzer,
		Fset:     fset,
		Files:    []*ast.File{file},
		Report:   func(analysis.Diagnostic) { t.Fatal("commentnumbers warns; it never fails a build") },
		Module:   module,
	}
	_, err = runCommentNumbers(pass)
	require.NoError(t, err)
	return logger.EmittedWarnings()
}
