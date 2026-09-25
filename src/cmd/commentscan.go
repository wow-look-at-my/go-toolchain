package cmd

import (
	"path/filepath"
	"strings"
	"unicode"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/slopfix/cardinal"
)

// commentscanSlashExts are extensions whose comments open on `//` or `/* */`.
var commentscanSlashExts = set.Of[string](".go", ".c", ".h", ".cc", ".cpp", ".cxx",
	".hpp", ".hh", ".rs", ".js", ".jsx", ".mjs", ".cjs", ".ts", ".mts", ".cts", ".tsx")

// commentscanHashExts are extensions whose comments open on `#`.
var commentscanHashExts = set.Of[string](".sh", ".bash", ".zsh", ".yml", ".yaml",
	".toml", ".ini", ".cfg", ".conf", ".dockerfile", ".mk", ".dats")

// commentscanHashNames are file names, extension apart, whose comments open on `#`.
var commentscanHashNames = set.Of[string]("dockerfile", "containerfile", "makefile", "justfile")

// Remedy is what every finding asks the author to do instead.
const commentscanRemedy = "a number in a comment is a count of what exists today, " +
	"and the edit that adds an item leaves it wrong: describe what the code does and let the reader count. " +
	"To point at a section of a spec or a document, cite its unique slug or its heading text, never its position: " +
	"the slug survives the edit that inserts a section above it, and a section sign (§) marks a citation that has no slug"

// commentscanHit is a number found in a comment, at the character a reader sees.
type commentscanHit struct {
	Number string
	Line   int
	Col    int
}

// commentscanSupported reports whether this rule reads a file of that name.
// A delimiter lookup, not a grammar: it does not need a parser per language.
func commentscanSupported(filename string) bool {
	base := strings.ToLower(filepath.Base(filename))
	if commentscanHashNames.Contains(base) {
		return true
	}
	ext := strings.ToLower(filepath.Ext(base))
	return commentscanSlashExts.Contains(ext) || commentscanHashExts.Contains(ext)
}

// commentscanUsesHash reports whether filename's comments open on `#`.
func commentscanUsesHash(filename string) bool {
	base := strings.ToLower(filepath.Base(filename))
	if commentscanHashNames.Contains(base) {
		return true
	}
	return commentscanHashExts.Contains(strings.ToLower(filepath.Ext(base)))
}

// commentscanIsGenerated reports whether the file carries the generated-code marker.
func commentscanIsGenerated(src string) bool {
	const marker, suffix = "// Code generated ", " DO NOT EDIT."
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, marker) && strings.HasSuffix(line, suffix) {
			return true
		}
	}
	return false
}

// commentscanCheck returns every number stated in a comment of a source file.
// It finds a comment by its delimiters, never by parsing the language, so it
// answers on a tree no compiler accepts.
func commentscanCheck(filename, src string) []commentscanHit {
	if commentscanIsGenerated(src) {
		return nil
	}
	var out []commentscanHit
	for _, c := range commentscanExtract(src, commentscanUsesHash(filename)) {
		if commentscanIsDirective(c.text) {
			continue
		}
		for _, found := range cardinal.Find(c.text, cardinal.Comment) {
			at := c.offset + found.Offset
			line, col := commentscanLineCol(src, at)
			out = append(out, commentscanHit{Number: found.Text, Line: line, Col: col})
		}
	}
	return out
}

// commentscanSpan is one comment's text and where it starts in the file.
type commentscanSpan struct {
	text   string
	offset int
}

// commentscanExtract finds every `//`, `/* */`, or `#` comment in src, skipping
// one that opens inside a quoted string or a rune literal.
func commentscanExtract(src string, hash bool) []commentscanSpan {
	var out []commentscanSpan
	inStr, inChar := false, false
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case inStr:
			if c == '\\' {
				i++
			} else if c == '"' {
				inStr = false
			}
		case inChar:
			if c == '\\' {
				i++
			} else if c == '\'' {
				inChar = false
			}
		case hash && c == '#':
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				end = len(src) - i
			}
			out = append(out, commentscanSpan{text: src[i : i+end], offset: i})
			i += end
		case !hash && c == '"':
			inStr = true
		case !hash && c == '\'':
			inChar = true
		case !hash && c == '/' && i+1 < len(src) && src[i+1] == '/':
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				end = len(src) - i
			}
			out = append(out, commentscanSpan{text: src[i : i+end], offset: i})
			i += end
		case !hash && c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(src[i:], "*/")
			if end < 0 {
				end = len(src) - i
			} else {
				end += 2
			}
			out = append(out, commentscanSpan{text: src[i : i+end], offset: i})
			i += end - 1
		}
	}
	return out
}

// commentscanIsDirective reports whether a line addresses a tool rather than a
// reader, such as //go:generate or #!/bin/sh.
func commentscanIsDirective(text string) bool {
	trimmed := strings.TrimSpace(text)
	rest, found := strings.CutPrefix(trimmed, "//")
	if !found {
		rest, found = strings.CutPrefix(trimmed, "#")
	}
	if !found || rest == "" || strings.HasPrefix(rest, " ") {
		return false
	}
	if strings.HasPrefix(rest, "!") {
		return true // shebang
	}
	name, _, found := strings.Cut(rest, ":")
	if !found || name == "" {
		return false
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// commentscanLineCol answers where a byte offset sits, counting from the top
// and the left of the file.
func commentscanLineCol(src string, at int) (line, col int) {
	if at > len(src) {
		at = len(src)
	}
	line = 1 + strings.Count(src[:at], "\n")
	start := strings.LastIndexByte(src[:at], '\n') + 1
	return line, at - start + 1
}
