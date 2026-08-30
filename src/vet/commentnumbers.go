package vet

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"unicode"

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

// commentNumbersRemedy is what every finding asks the author to do instead.
const commentNumbersRemedy = "a number in a comment is a count of what exists today, " +
	"and the edit that adds an item leaves it wrong: describe what the code does and let the reader count"

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
	for _, tok := range commentTokens(text) {
		if hit, ok := tokenNumber(text, tok); ok {
			found = append(found, hit)
		}
	}
	return found
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
func tokenNumber(text string, tok commentToken) (commentToken, bool) {
	if strings.Contains(tok.text, "://") {
		return commentToken{}, false // the digits of a URL are part of it
	}
	if isMoney(text, tok) {
		return commentToken{}, false // a sum of money is a value, not a count
	}
	if isHTTPStatus(text, tok) {
		return commentToken{}, false // a status code names a response, not a count
	}
	if hit, ok := digitNumber(tok); ok {
		return hit, true
	}
	if isQualifiedName(tok.text) {
		return commentToken{}, false // sync.Once and net/http name themselves
	}
	return wordNumber(tok)
}

// isMoney reports whether a currency sign sits directly against the token's
// digits. An amount is a value, and only the amount is exempt.
func isMoney(text string, tok commentToken) bool {
	if tok.offset == 0 || text[tok.offset-1] != '$' {
		return false
	}
	return unicode.IsDigit(rune(tok.text[0]))
}

// httpStatusPrefix is what marks digits as a status code rather than a count.
const httpStatusPrefix = "HTTP "

// httpStatusCodes are the codes the IANA HTTP Status Code Registry assigns. A
// code names a response, so it is a value the protocol fixed rather than a
// count of anything here. An unassigned number is not exempt: the registry is
// the whole carve-out.
var httpStatusCodes = set.Of(
	"100", "101", "102", "103",
	"200", "201", "202", "203", "204", "205", "206", "207", "208", "226",
	"300", "301", "302", "303", "304", "305", "306", "307", "308",
	"400", "401", "402", "403", "404", "405", "406", "407", "408", "409",
	"410", "411", "412", "413", "414", "415", "416", "417", "418",
	"421", "422", "423", "424", "425", "426", "428", "429", "431", "451",
	"500", "501", "502", "503", "504", "505", "506", "507", "508", "510", "511",
)

// isHTTPStatus reports whether the token is an assigned code behind the prefix.
func isHTTPStatus(text string, tok commentToken) bool {
	if !strings.HasSuffix(text[:tok.offset], httpStatusPrefix) {
		return false
	}
	return httpStatusCodes.Contains(tok.text)
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
