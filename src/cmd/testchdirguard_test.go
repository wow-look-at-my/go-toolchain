package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A hand-rolled chdir in a test leaves the whole package standing in a removed
// directory whenever its restore is skipped or its os.Getwd error is dropped,
// and every later os.Getwd in that process then fails. The casualty is some
// other test, so the report never names the file that caused it. t.Chdir
// restores the directory itself and fails the test that cannot chdir, so it is
// the only spelling this suite allows.
func TestNoTestCallsOsChdir(t *testing.T) {
	t.Serial()

	var offenders []string
	err := filepath.WalkDir(moduleRootForGuard(t), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || d.Name() == "build" || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		offenders = append(offenders, osChdirCalls(t, path)...)
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, offenders,
		"a test must take the working directory with t.Chdir, which restores it and fails when the chdir cannot happen")
}

// osChdirCalls reports the os.Chdir call sites in path, as file:line.
func osChdirCalls(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	require.NoError(t, err)

	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Chdir" {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "os" {
			found = append(found, fset.Position(call.Pos()).String())
		}
		return true
	})
	return found
}

// moduleRootForGuard locates this module's root from this file's own compiled
// path. The working directory cannot serve: the tests this guard covers are the
// ones that move it.
func moduleRootForGuard(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	require.True(t, ok, "the runtime does not know this file's path")

	root := filepath.Dir(filepath.Dir(filepath.Dir(self)))
	_, err := os.Stat(filepath.Join(root, "go.mod"))
	require.NoError(t, err, "the guard found no module at %s, so it scanned nothing", root)
	return root
}
