package vet

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/gomod"
	"golang.org/x/tools/go/analysis"
)

// CommentNumbersAnalyzer reports a number in a comment, in digits or in words.
// A comment that counts what sits below it is wrong the moment an item is
// added. Always a warning; the budget fails the build. Depth: docs/VET.md
var CommentNumbersAnalyzer = &analysis.Analyzer{
	Name:       "commentnumbers",
	Doc:        "reports a number in a comment; name what the code does instead of counting it",
	Run:        runCommentNumbers,
	ResultType: reflect.TypeOf([]*ASTFixes{}),
}

// numberWords are the numbers spelled as words: the cardinals, the ordinals
// that index a list, and the words for a repeat count. A word joined to other
// letters is a name (oneShot, someone), so only a whole word counts.
var numberWords = set.Of(
	"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine",
	"ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen",
	"seventeen", "eighteen", "nineteen", "twenty", "thirty", "forty", "fifty",
	"sixty", "seventy", "eighty", "ninety", "hundred", "thousand", "million",
	"billion", "trillion", "dozen",
	"once", "twice", "thrice",
	"first", "second", "third", "fourth", "fifth", "sixth", "seventh", "eighth",
	"ninth", "tenth", "eleventh", "twelfth", "thirteenth", "fourteenth",
	"fifteenth", "sixteenth", "seventeenth", "eighteenth", "nineteenth",
	"twentieth", "thirtieth", "fortieth", "fiftieth", "sixtieth", "seventieth",
	"eightieth", "ninetieth", "hundredth", "thousandth",
)

// commentNumbersRemedy is what every finding asks the author to do instead. A
// reference to a numbered section is the case rewriting the sentence does not
// cover, so the remedy also names the slug that survives a renumbering edit.
const commentNumbersRemedy = "a number in a comment is a count of what exists today, " +
	"and the edit that adds an item leaves it wrong: describe what the code does and let the reader count. " +
	"To point at a section of a spec or a document, cite its unique slug or its heading text, never its position: " +
	"the slug survives the edit that inserts a section above it, and a section sign (§) marks a citation that has no slug"

// commentNumbersWarned records file:line of every warning; each package variant that walks a file warns per site.
var commentNumbersWarned sync.Map

// resetCommentNumbersWarnings forgets prior warnings, so a re-run after a fix reports its sites again.
func resetCommentNumbersWarnings() { commentNumbersWarned.Clear() }

// A finding is always a warning, in every module. A comment that counts is
// stale prose, not broken code, so it must not fail a build by itself -- but
// they pile up fast, so the warnings budget (docs/WARNINGS-GATE.md) is what
// turns a repo full of them red.
func runCommentNumbers(pass *analysis.Pass) (any, error) {
	report := func(pos token.Pos, format string, args ...any) {
		warnAt(&commentNumbersWarned, pass, pos, format, args...)
	}
	for _, file := range pass.Files {
		if skipCommentNumbers(pass, file) {
			continue
		}
		for _, group := range file.Comments {
			for _, c := range group.List {
				if _, isDirective := ast.ParseDirective(c.Slash, c.Text); isDirective {
					continue
				}
				for _, found := range commentNumbers(c.Text) {
					report(c.Slash+token.Pos(found.offset), "%q is a number in a comment: %s", found.text, commentNumbersRemedy)
				}
			}
		}
	}
	return []*ASTFixes(nil), nil
}

// skipCommentNumbers reports whether a file's comments belong to somebody else:
// a nested module keeps its upstream text, and a generated file is written by a
// program that no rule here reaches.
func skipCommentNumbers(pass *analysis.Pass, file *ast.File) bool {
	filename := pass.Fset.Position(file.Pos()).Filename
	return gomod.IsNestedModule(filepath.Dir(filename)) || commentSpanIsGenerated(file)
}

// commentToken is a run of name characters and where it starts in the comment.
type commentToken struct {
	offset int
	text   string
}

// nameMarkers join an identifier, an import path or a label into a name.
const nameMarkers = "._/:"

// commentNumbers finds every number in the text of a single comment line.
func commentNumbers(text string) []commentToken {
	var found []commentToken
	toks := commentTokens(text)
	for i, tok := range toks {
		if isHTTPStatus(toks, i) || isSectionRef(text, tok) || isMoney(text, tok) {
			continue
		}
		if hit, ok := tokenNumber(tok); ok {
			found = append(found, hit)
		}
	}
	return found
}

// httpStatusPrefix is the word that names the digits after it as a status code.
const httpStatusPrefix = "HTTP"

// statusCodeDigits is the width of an HTTP status code.
const statusCodeDigits = len("500")

// isHTTPStatus reports whether the token at i is an HTTP status code. The
// prefix is what separates the digits of a protocol answer from a count
// wearing the same shape.
func isHTTPStatus(toks []commentToken, i int) bool {
	if i == 0 {
		return false
	}
	prefix := strings.Trim(toks[i-1].text, nameMarkers)
	return strings.EqualFold(prefix, httpStatusPrefix) && isStatusCode(toks[i].text)
}

// isStatusCode reports whether text is the bare digits of a status code. A
// name marker at either end is the punctuation of the sentence, since a token
// keeps the period that ends it.
func isStatusCode(text string) bool {
	text = strings.Trim(text, nameMarkers)
	if len(text) != statusCodeDigits {
		return false
	}
	for _, r := range text {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// sectionSign marks the number after it as a citation of a section.
const sectionSign = '§'

// isSectionRef reports whether the token sits behind a section sign, which
// cites a section of a document instead of counting anything here.
func isSectionRef(text string, tok commentToken) bool {
	before := strings.TrimRight(text[:tok.offset], " \t")
	last, size := utf8.DecodeLastRuneInString(before)
	return size > 0 && last == sectionSign
}

// currencySign marks the digits against it as an amount rather than a count.
const currencySign = '$'

// isMoney reports whether the token opens with digits carrying a currency sign
// directly against them. An amount is a value, and only the amount is exempt.
func isMoney(text string, tok commentToken) bool {
	last, size := utf8.DecodeLastRuneInString(text[:tok.offset])
	if size == 0 || last != currencySign {
		return false
	}
	return unicode.IsDigit(rune(tok.text[0]))
}

// commentTokens splits a comment into runs of name characters, so a URL, an
// import path and a version each stay whole.
func commentTokens(text string) []commentToken {
	var toks []commentToken
	start := -1
	for i, r := range text {
		if isNameRune(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			toks = append(toks, commentToken{start, text[start:i]})
			start = -1
		}
	}
	if start >= 0 {
		toks = append(toks, commentToken{start, text[start:]})
	}
	return toks
}

// isNameRune reports whether r can sit inside a technical name.
func isNameRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(nameMarkers+"-", r)
}

// tokenNumber reports the number a token carries, if it carries any.
func tokenNumber(tok commentToken) (commentToken, bool) {
	if strings.Contains(tok.text, "://") {
		return commentToken{}, false // the digits of a URL are part of it
	}
	if hit, ok := digitNumber(tok); ok {
		return hit, true
	}
	if isQualifiedName(tok.text) {
		return commentToken{}, false // sync.Once and net/http name themselves
	}
	return wordNumber(tok)
}

// isQualifiedName reports whether a marker sits BETWEEN name characters, which
// is what separates an identifier from a sentence that ends on a word.
func isQualifiedName(text string) bool {
	runes := []rune(text)
	for i := 1; i < len(runes)-1; i++ {
		if !strings.ContainsRune(nameMarkers, runes[i]) {
			continue
		}
		if isWordRune(runes[i-1]) && isWordRune(runes[i+1]) {
			return true
		}
	}
	return false
}

// isWordRune reports whether r is a letter or a digit.
func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// digitNumber reports the digits a token spells as a number. A digit touching a
// letter is part of a name (sha256, amd64, 10ms); an ordinal suffix makes it a
// number again.
func digitNumber(tok commentToken) (commentToken, bool) {
	runes, offsets := runesOf(tok.text)
	for i := 0; i < len(runes); i++ {
		if !unicode.IsDigit(runes[i]) {
			continue
		}
		end := i
		for end < len(runes) && unicode.IsDigit(runes[end]) {
			end++
		}
		if !touchesLetter(runes, i, end) || hasOrdinalSuffix(runes, end) {
			return commentToken{tok.offset + offsets[i], string(runes[i:end])}, true
		}
		i = end
	}
	return commentToken{}, false
}

// wordNumber reports the number a token spells in letters.
func wordNumber(tok commentToken) (commentToken, bool) {
	runes, offsets := runesOf(tok.text)
	for i := 0; i < len(runes); i++ {
		if !unicode.IsLetter(runes[i]) {
			continue
		}
		end := i
		for end < len(runes) && unicode.IsLetter(runes[end]) {
			end++
		}
		word := string(runes[i:end])
		if numberWords.Contains(strings.ToLower(word)) {
			return commentToken{tok.offset + offsets[i], word}, true
		}
		i = end
	}
	return commentToken{}, false
}

// runesOf splits s into its runes alongside each rune's byte offset, so a
// finding can point at the character a reader sees.
func runesOf(s string) ([]rune, []int) {
	runes := make([]rune, 0, len(s))
	offsets := make([]int, 0, len(s))
	for i, r := range s {
		runes = append(runes, r)
		offsets = append(offsets, i)
	}
	return runes, offsets
}

// touchesLetter reports whether a letter sits directly against the run.
func touchesLetter(runes []rune, start, end int) bool {
	return (start > 0 && unicode.IsLetter(runes[start-1])) ||
		(end < len(runes) && unicode.IsLetter(runes[end]))
}

// ordinalSuffixes are what a digit wears when it is still a number.
var ordinalSuffixes = set.Of("st", "nd", "rd", "th")

// hasOrdinalSuffix reports whether an ordinal suffix, and nothing else, follows
// the digits at end.
func hasOrdinalSuffix(runes []rune, end int) bool {
	suffix := end + len("st")
	if suffix > len(runes) {
		return false
	}
	if suffix < len(runes) && unicode.IsLetter(runes[suffix]) {
		return false
	}
	return ordinalSuffixes.Contains(strings.ToLower(string(runes[end:suffix])))
}
