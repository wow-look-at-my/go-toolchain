package vet

import (
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"github.com/wow-look-at-my/go-toolchain/src/gomod"
	"golang.org/x/tools/go/analysis"
)

// CommentSpanAnalyzer reports a comment bigger than the code it documents,
// by lines or by chars past commentSpanCharFloor. A directive line, and the
// package doc, are never measured.
var CommentSpanAnalyzer = &analysis.Analyzer{
	Name:       "commentspan",
	Doc:        "reports a comment longer than the code it documents",
	Run:        runCommentSpan,
	ResultType: reflect.TypeOf([]*ASTFixes{}),
}

// commentSpanCharFloor is the char allowance every comment gets, regardless of code size.
const commentSpanCharFloor = 120

// commentSpanWarned dedupes warnings per file:line, since every package variant walks the same file. Reset per run.
var commentSpanWarned sync.Map

func resetCommentSpanWarnings() { commentSpanWarned.Clear() }

// Duplicates filelength.go's generated-file marker: src/cmd imports src/vet, so the reverse import would cycle.
var commentSpanGeneratedRe = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

func runCommentSpan(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		checkCommentSpanFile(pass, file)
	}
	return []*ASTFixes(nil), nil
}

// checkCommentSpanFile measures every comment group in file against the node
// ast.NewCommentMap associates it with.
func checkCommentSpanFile(pass *analysis.Pass, file *ast.File) {
	if len(file.Comments) == 0 {
		return
	}
	filename := pass.Fset.Position(file.Pos()).Filename
	if gomod.IsNestedModule(filepath.Dir(filename)) || commentSpanIsGenerated(file) {
		return
	}
	src, err := os.ReadFile(filename)
	if err != nil {
		return
	}

	nodeOf := make(map[*ast.CommentGroup]ast.Node, len(file.Comments))
	for node, groups := range ast.NewCommentMap(pass.Fset, file, file.Comments) {
		for _, g := range groups {
			nodeOf[g] = node
		}
	}

	for _, g := range file.Comments {
		if g == file.Doc {
			continue
		}
		checkCommentGroup(pass, src, g, nodeOf[g])
	}
}

// commentSpanIsGenerated reports whether file's header carries the canonical
// generated-file marker.
func commentSpanIsGenerated(file *ast.File) bool {
	for _, g := range file.Comments {
		if g.Pos() > file.Package {
			return false
		}
		for _, c := range g.List {
			if commentSpanGeneratedRe.MatchString(c.Text) {
				return true
			}
		}
	}
	return false
}

// checkCommentGroup warns when g is bigger than node, the thing it documents.
func checkCommentGroup(pass *analysis.Pass, src []byte, g *ast.CommentGroup, node ast.Node) {
	if node == nil {
		return
	}
	commentText, ok := commentSpanText(g)
	if !ok {
		return // every line was a directive
	}
	start := pass.Fset.Position(node.Pos())
	end := pass.Fset.Position(node.End())
	if start.Offset < 0 || end.Offset > len(src) || start.Offset > end.Offset {
		return
	}
	thingText := string(src[start.Offset:end.Offset])

	cLines, cChars := commentSpanMeasure(commentText)
	tLines, tChars := commentSpanMeasure(thingText)
	charLimit := max(commentSpanCharFloor, tChars)

	var msg strings.Builder
	if cLines > tLines {
		fmt.Fprintf(&msg, "\n  %d comment lines > %d code lines = %d lines too long", cLines, tLines, cLines-tLines)
	}
	if cChars > charLimit {
		fmt.Fprintf(&msg, "\n %d comment chars > %d code chars = %d chars too long", cChars, charLimit, cChars-charLimit)
	}
	if msg.Len() == 0 {
		return
	}
	warnAt(&commentSpanWarned, pass, g.Pos(), "comment is longer than the code it documents.%s", msg.String())
}

// commentSpanText joins a comment group's non-directive lines, so a //go:build
// or //go:generate line inside an otherwise-prose group is never measured.
func commentSpanText(g *ast.CommentGroup) (string, bool) {
	var sb strings.Builder
	kept := false
	for _, c := range g.List {
		if _, isDirective := ast.ParseDirective(c.Slash, c.Text); isDirective {
			continue
		}
		if kept {
			sb.WriteByte('\n')
		}
		sb.WriteString(c.Text)
		kept = true
	}
	return sb.String(), kept
}

// commentSpanMeasure counts non-blank lines and non-whitespace chars in s, so
// indentation carries no cost. Used on both a comment and its code, so the
// counts are directly comparable.
func commentSpanMeasure(s string) (lines, chars int) {
	for _, line := range strings.Split(s, "\n") {
		hasContent := false
		for _, r := range line {
			if unicode.IsSpace(r) {
				continue
			}
			hasContent = true
			chars++
		}
		if hasContent {
			lines++
		}
	}
	return lines, chars
}
