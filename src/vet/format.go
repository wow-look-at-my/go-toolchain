package vet

import (
	"bufio"
	"bytes"
	"fmt"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/gomod"
)

var generatedCodeRe = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

// RunGofmt checks that all Go source files in the current directory tree are
// formatted, routing every unformatted file through ed: a fix-mode editor
// rewrites it in place, a check-mode (CI) editor records it as a violation.
// Returns whether any file was written.
func RunGofmt(ed Editor) (bool, error) {
	var anyWrote bool

	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name != "." && (strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata") {
				return filepath.SkipDir
			}
			// A nested module's files are not this module's to reformat —
			// they must stay byte-identical to their upstream.
			if gomod.IsNestedModule(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if isGeneratedGoSource(src) {
			return nil
		}
		formatted, err := format.Source(src)
		if err != nil {
			// Parse errors are caught by go vet; skip unparse-able files here.
			return nil
		}
		formatted = revertDocCommentSmartQuotes(formatted)
		if !bytes.Equal(src, formatted) {
			wrote, err := ed.Require(path, formatted, "not gofmt-formatted")
			if err != nil {
				return err
			}
			if wrote {
				anyWrote = true
			}
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("gofmt: %w", err)
	}

	return anyWrote, nil
}

// gofmt turns doubled backtick/apostrophe into curly quotes; \u escapes here keep the file ASCII.
const (
	docQuoteLeft  = "\u201c" // gofmt synthesizes this from a doubled backtick
	docQuoteRight = "\u201d" // gofmt synthesizes this from a doubled apostrophe
)

// revertDocCommentSmartQuotes reverts gofmt's smart-quote substitution back to
// the ASCII digraphs, restoring U+201C to a doubled backtick and U+201D to a
// doubled apostrophe wherever they appear inside a Go comment. This is curative,
// not merely preventive: gofmt's doc-comment formatter is the ONLY thing that
// produces these runes in Go source, and only inside comments, so a curly quote
// in a comment is always a gofmt artifact -- no author types one there by hand.
// It therefore also heals comments that an earlier, unfixed run already
// corrupted, not just the file currently being formatted.
//
// The revert is scoped to comment spans via a parse of the (gofmt-valid) source,
// so curly quotes inside string or rune literals -- where they are real program
// data, not prose -- are left untouched. A fast path skips the parse entirely for
// the overwhelming majority of files, which contain no curly quotes at all; an
// unparseable input is returned unchanged rather than risk a blind global
// replace that could reach a literal.
func revertDocCommentSmartQuotes(formatted []byte) []byte {
	left, right := []byte(docQuoteLeft), []byte(docQuoteRight)
	if !bytes.Contains(formatted, left) && !bytes.Contains(formatted, right) {
		return formatted // no smart quotes anywhere: nothing to revert
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", formatted, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return formatted // unparseable: never revert outside a known comment span
	}

	var b bytes.Buffer
	b.Grow(len(formatted))
	prev := 0
	for _, group := range file.Comments {
		for _, c := range group.List {
			start := fset.Position(c.Pos()).Offset
			end := fset.Position(c.End()).Offset
			if start < prev || end > len(formatted) {
				continue // defensive: only rewrite cleanly-mapped, in-order spans
			}
			b.Write(formatted[prev:start]) // code and literals, verbatim
			seg := bytes.ReplaceAll(formatted[start:end], left, []byte("``"))
			seg = bytes.ReplaceAll(seg, right, []byte("''"))
			b.Write(seg)
			prev = end
		}
	}
	b.Write(formatted[prev:])
	return b.Bytes()
}

// canonicalizeGoSource turns printed -- the raw output of go/printer for a
// rewritten AST -- into gofmt-canonical bytes. go/printer's default mode uses
// tabs for both indentation AND alignment, so this reruns go/format to restore
// the canonical "tabs to indent, spaces to align" style (plus import sorting and
// number normalization), then reverts gofmt's doc-comment smart-quote
// substitution. Every vet rewriter routes its output through this so they all
// emit identical canonical formatting and never corrupt literal quotes in
// comments. If printed does not parse (an unexpected, transient bad render) the
// revert is a no-op and printed is returned, leaving the unparseable file for go
// vet to report rather than making it worse.
func canonicalizeGoSource(printed []byte) []byte {
	formatted, err := format.Source(printed)
	if err != nil {
		return revertDocCommentSmartQuotes(printed)
	}
	return revertDocCommentSmartQuotes(formatted)
}

func isGeneratedGoSource(src []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(src))
	for scanner.Scan() {
		line := scanner.Text()
		if generatedCodeRe.MatchString(line) {
			return true
		}
		if strings.HasPrefix(line, "package ") {
			return false
		}
	}
	return false
}
