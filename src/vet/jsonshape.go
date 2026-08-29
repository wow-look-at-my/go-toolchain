package vet

import "strings"

// jsonHole stands for an interpolated value while text is classified. Go
// source cannot hold this byte, so it never collides with the text it replaces.
const jsonHole = '\x00'

// isJSONDocument reports whether text is JSON with values dropped into it.
// Two things must hold. Outside its quoted strings the text spells nothing but
// JSON syntax, so prose that quotes an example stays silent. And it shows one
// of three shapes: a quoted key, an array holding a string or an object, or an
// object holding a string. Depth: docs/VET.md
func isJSONDocument(text string) bool {
	outside, key := scanJSONStrings(text)
	if !onlyJSONSyntax(outside) {
		return false
	}
	if key {
		return true
	}
	trimmed := strings.TrimSpace(text)
	return (bracketed(trimmed, '[', ']') && strings.ContainsAny(trimmed, "\"{")) ||
		(bracketed(trimmed, '{', '}') && strings.Contains(trimmed, "\""))
}

// scanJSONStrings splits text at its quoted strings. It returns the text
// outside them and whether any of them is a key, which is a closed string that
// a colon follows. A string the text never closes is a fragment, and it ends
// the scan.
func scanJSONStrings(text string) (outside string, key bool) {
	var out strings.Builder
	for i := 0; i < len(text); {
		if text[i] != '"' {
			out.WriteByte(text[i])
			i++
			continue
		}
		end := closingQuote(text, i+1)
		if end < 0 {
			return out.String(), key
		}
		i = end + 1
		for i < len(text) && isJSONSpace(text[i]) {
			i++
		}
		if i < len(text) && text[i] == ':' {
			key = true
		}
	}
	return out.String(), key
}

// closingQuote reports the index of the quote that closes a string opened
// before start, or -1 when the text ends first.
func closingQuote(text string, start int) int {
	for i := start; i < len(text); i++ {
		if text[i] == '\\' {
			i++
			continue
		}
		if text[i] == '"' {
			return i
		}
	}
	return -1
}

// onlyJSONSyntax reports whether s, the text outside the quoted strings,
// spells JSON structure and nothing else.
func onlyJSONSyntax(s string) bool {
	for _, word := range []string{"true", "false", "null"} {
		s = strings.ReplaceAll(s, word, "")
	}
	return strings.IndexFunc(s, func(r rune) bool { return !isJSONSyntaxRune(r) }) < 0
}

// isJSONSyntaxRune reports whether r can stand outside a string in JSON: the
// punctuation, a number, whitespace, or a hole a value fills.
func isJSONSyntaxRune(r rune) bool {
	if r == jsonHole || (r >= '0' && r <= '9') {
		return true
	}
	return strings.ContainsRune("{}[]:,+-.eE \t\r\n", r)
}

// isJSONSpace reports whether b separates JSON tokens.
func isJSONSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\r' || b == '\n' }

// bracketed reports whether s opens and closes with the given pair.
func bracketed(s string, opens, closes byte) bool {
	return len(s) >= 2 && s[0] == opens && s[len(s)-1] == closes
}

// verbFlags are the bytes a format verb may carry between its percent sign and
// its letter: the flags, a width, a precision and an argument index.
const verbFlags = "+-# 0123456789.*[]"

// normalizeVerbs replaces each format verb with a hole and reports how many it
// found. A doubled percent sign prints one and interpolates nothing, so it is
// not a verb.
func normalizeVerbs(format string) (string, int) {
	var out strings.Builder
	verbs := 0
	for i := 0; i < len(format); {
		if format[i] != '%' {
			out.WriteByte(format[i])
			i++
			continue
		}
		end := i + 1
		for end < len(format) && strings.IndexByte(verbFlags, format[end]) >= 0 {
			end++
		}
		if end >= len(format) {
			out.WriteByte('%')
			break
		}
		if format[end] != '%' {
			out.WriteByte(jsonHole)
			verbs++
		}
		i = end + 1
	}
	return out.String(), verbs
}

// normalizeActions replaces each template action with a hole and reports how
// many it found. An unclosed action ends the text.
func normalizeActions(text string) (string, int) {
	var out strings.Builder
	actions := 0
	for {
		open := strings.Index(text, "{{")
		if open < 0 {
			out.WriteString(text)
			return out.String(), actions
		}
		out.WriteString(text[:open])
		closed := strings.Index(text[open:], "}}")
		if closed < 0 {
			return out.String(), actions
		}
		out.WriteByte(jsonHole)
		actions++
		text = text[open+closed+2:]
	}
}
