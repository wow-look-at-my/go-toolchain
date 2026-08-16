package vet

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"golang.org/x/tools/go/analysis"
)

// runWriteRunsOn analyzes one source file and returns the line of every
// warning the check emitted, in emission order.
func runWriteRunsOn(t *testing.T, src string) []int {
	t.Helper()
	resetWriteRunWarnings()
	logger.ResetWarnCount()
	t.Cleanup(logger.ResetWarnCount)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "/pkg/write.go", src, parser.ParseComments)
	require.NoError(t, err)

	pass := &analysis.Pass{
		Analyzer: WriteRunsAnalyzer,
		Fset:     fset,
		Files:    []*ast.File{file},
		Report:   func(analysis.Diagnostic) { t.Fatal("writeruns warns; it never fails a build") },
	}
	_, err = runWriteRuns(pass)
	require.NoError(t, err)

	// WarnFile records the file it annotates ahead of the message, so the
	// position the message itself carries is the LAST one in the text.
	var lines []int
	for _, msg := range logger.EmittedWarnings() {
		at := strings.LastIndex(msg, "/pkg/write.go:")
		require.GreaterOrEqual(t, at, 0, "warning names the file: %s", msg)
		num, _, _ := strings.Cut(msg[at+len("/pkg/write.go:"):], ":")
		line, err := strconv.Atoi(num)
		require.NoError(t, err, "warning names a line: %s", msg)
		lines = append(lines, line)
	}
	return lines
}

// TestWriteRunsWarnsPastTheSecondWrite runs the check over the shape it exists
// for: a shell script spelled out one write at a time. The first two writes
// are free and the last three are the document, so the bottom three lines
// warn.
func TestWriteRunsWarnsPastTheSecondWrite(t *testing.T) {
	const src = `package ape

import (
	"fmt"
	"strings"
)

func emit(script *strings.Builder, apeRunDir string) {
	fmt.Fprintf(script, "  c=%s/$k\n", apeRunDir)
	script.WriteString("  p=\"$c/${0##*/}\"\n")
	script.WriteString("  if [ ! -x \"$p\" ]; then\n")
	script.WriteString("    (umask 077; mkdir -p \"$c\") || exit 121\n")
	script.WriteString("    cp \"$o\" \"$p.$$\" || exit 121\n")
}
`
	require.Equal(t, []int{11, 12, 13}, runWriteRunsOn(t, src))
}

// TestWriteRunsNamesTheRemedy verifies the warning says what to write instead.
// A count with no remedy leaves the reader to guess.
func TestWriteRunsNamesTheRemedy(t *testing.T) {
	const src = `package ape

import "strings"

func emit(b *strings.Builder) {
	b.WriteString("a")
	b.WriteString("b")
	b.WriteString("c")
}
`
	require.Equal(t, []int{8}, runWriteRunsOn(t, src))
	warnings := logger.EmittedWarnings()
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "text/template")
	assert.Contains(t, warnings[0], "write 3 in a row to b")
}

// TestWriteRunsBoundaries covers what starts a run, what ends one, and what is
// not a write at all.
func TestWriteRunsBoundaries(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		lines []int
	}{
		{
			name: "two writes are free",
			body: `b.WriteString("a")
	b.WriteString("b")`,
		},
		{
			name: "another statement ends the run",
			body: `b.WriteString("a")
	b.WriteString("b")
	n++
	b.WriteString("c")
	b.WriteString("d")`,
		},
		{
			name: "a second writer starts its own run",
			body: `b.WriteString("a")
	other.WriteString("a")
	b.WriteString("b")
	other.WriteString("b")`,
		},
		{
			name: "fmt and the method mix in one run",
			body: `fmt.Fprintln(b, "a")
	b.WriteString("b")
	fmt.Fprintf(b, "%d", n)
	b.WriteRune('c')
	b.WriteByte('d')`,
			lines: []int{11, 12, 13},
		},
		{
			name: "a field names its own writer",
			body: `s.buf.WriteString("a")
	s.buf.WriteString("b")
	s.buf.WriteString("c")`,
			lines: []int{11},
		},
		{
			name: "a call that is not a write is not counted",
			body: `b.WriteString("a")
	b.WriteString("b")
	b.Reset()
	b.WriteString("c")`,
		},
		{
			name: "a checked write is not a dropped write",
			body: `b.WriteString("a")
	b.WriteString("b")
	if _, err := b.WriteString("c"); err != nil {
		return
	}`,
		},
		{
			name: "a loop body holds its own run",
			body: `for range 3 {
		b.WriteString("a")
		b.WriteString("b")
		b.WriteString("c")
	}`,
			lines: []int{12},
		},
		{
			name: "a case clause holds its own run",
			body: `switch n {
	case 1:
		b.WriteString("a")
		b.WriteString("b")
		b.WriteString("c")
	}`,
			lines: []int{13},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// The body starts on line 9, which every wanted line above counts from.
			src := fmt.Sprintf(`package ape

import (
	"fmt"
	"strings"
)

func emit(b, other *strings.Builder, s struct{ buf *strings.Builder }, n int) {
	%s
	_, _ = fmt.Fprint(b, n)
}
`, c.body)
			assert.Equal(t, c.lines, runWriteRunsOn(t, src))
		})
	}
}

// TestWriteRunsSpendsOneWarningPerSite verifies the file:line deduplication.
// go/packages loads a package up to four ways and every variant walks the same
// file, so a site that warned four times would spend a quarter of the warnings
// budget on one line.
func TestWriteRunsSpendsOneWarningPerSite(t *testing.T) {
	const src = `package ape

import "strings"

func emit(b *strings.Builder) {
	b.WriteString("a")
	b.WriteString("b")
	b.WriteString("c")
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "/pkg/write.go", src, parser.ParseComments)
	require.NoError(t, err)
	pass := &analysis.Pass{
		Analyzer: WriteRunsAnalyzer,
		Fset:     fset,
		Files:    []*ast.File{file},
		Report:   func(analysis.Diagnostic) { t.Fatal("writeruns warns; it never fails a build") },
	}

	resetWriteRunWarnings()
	logger.ResetWarnCount()
	t.Cleanup(logger.ResetWarnCount)

	for range 4 {
		_, err = runWriteRuns(pass)
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, logger.WarnCount())

	// A later vet run reports the site again.
	resetWriteRunWarnings()
	_, err = runWriteRuns(pass)
	require.NoError(t, err)
	require.EqualValues(t, 2, logger.WarnCount())
}
