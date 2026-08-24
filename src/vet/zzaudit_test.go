package vet

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"golang.org/x/tools/go/analysis"
)

// TestZZCommentSpanAudit is a scratch aid, not part of the committed suite:
// it walks every real .go file in the repo and dumps every commentspan
// warning to a local file, bypassing go-toolchain's own truncated console
// output. Removed before commit.
func TestZZCommentSpanAudit(t *testing.T) {
	root, err := filepath.Abs("../..")
	require := func(cond bool, msg string) {
		if !cond {
			t.Fatal(msg)
		}
	}
	require(err == nil, "abs failed")

	var files []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name != "." && (strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" || name == "node_modules" || name == "build") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	require(err == nil, "walk failed")

	resetCommentSpanWarnings()
	logger.ResetWarnCount()
	t.Cleanup(logger.ResetWarnCount)

	fset := token.NewFileSet()
	for _, f := range files {
		astFile, perr := parser.ParseFile(fset, f, nil, parser.ParseComments)
		if perr != nil {
			continue
		}
		pass := &analysis.Pass{Fset: fset, Files: []*ast.File{astFile}, Report: func(analysis.Diagnostic) {}}
		_, _ = runCommentSpan(pass)
	}

	warnings := logger.EmittedWarnings()
	sort.Slice(warnings, func(i, j int) bool { return warnings[i].Message < warnings[j].Message })

	var sb strings.Builder
	sb.WriteString("files scanned: " + strconv.Itoa(len(files)))
	sb.WriteString("\ndistinct warnings: " + strconv.Itoa(len(warnings)) + "\n\n")
	for _, w := range warnings {
		sb.WriteString(w.Message)
		sb.WriteString("\n---\n")
	}
	require(os.WriteFile("/tmp/commentspan-audit.txt", []byte(sb.String()), 0o644) == nil, "write failed")
}
