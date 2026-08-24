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

	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"golang.org/x/tools/go/analysis"
)

// TestZZCommentSpanAudit is a scratch aid, not part of the committed suite:
// it walks every real .go file in the repo and dumps every commentspan
// warning to a local file, bypassing go-toolchain's own truncated console
// output and the 200-entry EmittedWarnings retention cap (drained per file,
// so no single file's warnings can push an earlier file's out). Removed
// before commit.
func TestZZCommentSpanAudit(t *testing.T) {
	root, err := filepath.Abs("../..")
	require.NoError(t, err)

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
		if strings.HasSuffix(path, ".go") && filepath.Base(path) != "zzaudit_test.go" {
			files = append(files, path)
		}
		return nil
	})
	require.NoError(t, err)

	var all []string
	for _, f := range files {
		resetCommentSpanWarnings()
		logger.ResetWarnCount()

		fset := token.NewFileSet()
		astFile, perr := parser.ParseFile(fset, f, nil, parser.ParseComments)
		if perr != nil {
			continue
		}
		pass := &analysis.Pass{Fset: fset, Files: []*ast.File{astFile}, Report: func(analysis.Diagnostic) {}}
		_, _ = runCommentSpan(pass)

		for _, w := range logger.EmittedWarnings() {
			all = append(all, w.Message)
		}
	}
	t.Cleanup(logger.ResetWarnCount)

	sort.Strings(all)

	var sb strings.Builder
	sb.WriteString("files scanned: " + strconv.Itoa(len(files)))
	sb.WriteString("\ndistinct warnings: " + strconv.Itoa(len(all)) + "\n\n")
	for _, msg := range all {
		sb.WriteString(msg)
		sb.WriteString("\n---\n")
	}
	require.NoError(t, os.WriteFile("/tmp/commentspan-audit.txt", []byte(sb.String()), 0o644))
}
