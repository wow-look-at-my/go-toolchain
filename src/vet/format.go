package vet

import (
	"bufio"
	"bytes"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
		formatted = revertDocCommentSmartQuotes(src, formatted)
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

// gofmt's doc-comment formatter (go/doc/comment, run since Go 1.19 whenever
// go/printer reformats a top-level doc comment) rewrites TeX-style quote
// digraphs into Unicode "smart" quotes: a doubled backtick becomes U+201C
// (left double quote) and a doubled apostrophe becomes U+201D (right double
// quote). That silently rewrites literal ASCII an author typed -- e.g. a POSIX
// shell single-quote escape sequence has its doubled apostrophe turned into one
// U+201D, which is wrong -- and turns an ASCII-only file into multi-byte UTF-8.
// These are the only two substitutions it performs (lone quotes are left alone),
// and it never touches a curly quote that is already present. The values use
// \u escapes so this file itself stays printable ASCII.
const (
	docQuoteLeft  = "\u201c" // gofmt synthesizes this from a doubled backtick
	docQuoteRight = "\u201d" // gofmt synthesizes this from a doubled apostrophe
)

// revertDocCommentSmartQuotes undoes gofmt's doc-comment smart-quote
// substitution in formatted, restoring the ASCII digraphs the author actually
// typed while keeping every other gofmt fix (tab indentation, space alignment,
// import sorting, doc reflow). gofmt is the only thing that introduces these
// runes, so when src does not already contain a given curly quote, every
// occurrence in formatted was synthesized and is safe to revert. The revert is
// therefore guarded per rune on src: a curly quote the author typed themselves
// (src already has it) is left untouched rather than risk corrupting intentional
// text. The transform is idempotent -- reverting an already-reverted file is a
// no-op -- so the local fix and the CI check agree on the canonical form.
func revertDocCommentSmartQuotes(src, formatted []byte) []byte {
	left, right := []byte(docQuoteLeft), []byte(docQuoteRight)
	hasLeft := bytes.Contains(formatted, left)
	hasRight := bytes.Contains(formatted, right)
	if !hasLeft && !hasRight {
		return formatted // gofmt introduced no smart quotes: nothing to revert
	}
	out := formatted
	if hasLeft && !bytes.Contains(src, left) {
		out = bytes.ReplaceAll(out, left, []byte("``"))
	}
	if hasRight && !bytes.Contains(src, right) {
		out = bytes.ReplaceAll(out, right, []byte("''"))
	}
	return out
}

// canonicalizeGoSource turns printed -- the raw output of go/printer for a
// rewritten AST -- into gofmt-canonical bytes. go/printer's default mode uses
// tabs for both indentation AND alignment, so this reruns go/format to restore
// the canonical "tabs to indent, spaces to align" style (plus import sorting and
// number normalization), then reverts gofmt's doc-comment smart-quote
// substitution using orig (the file's pre-edit bytes) as the guard. Every vet
// rewriter routes its output through this so they all emit identical canonical
// formatting and never corrupt literal quotes in doc comments. If printed does
// not parse (an unexpected, transient bad render) it falls back to printed with
// only the quote revert applied, leaving the unparseable file for go vet to
// report rather than making it worse.
func canonicalizeGoSource(printed, orig []byte) []byte {
	formatted, err := format.Source(printed)
	if err != nil {
		return revertDocCommentSmartQuotes(orig, printed)
	}
	return revertDocCommentSmartQuotes(orig, formatted)
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
