package vet

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"

	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-containers/set"
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/packages"
)

func TestRedundantCastAnalyzer(t *testing.T) {
	t.Serial() // See TestBannedOutputAnalyzer.
	testdata, err := filepath.Abs("testdata")
	require.Nil(t, err)
	analysistest.Run(t, testdata, RedundantCastAnalyzer, "redundantcast")
}

// An analyzer reports through logger, whose warning list is process state, so
// these read a committed fixture and still run serially.
func TestAssertLintAnalyzer(t *testing.T) {
	t.Serial()
	testdata, err := filepath.Abs("testdata")
	require.Nil(t, err)
	analysistest.Run(t, testdata, AssertLintAnalyzer, "assertlint")
}

func TestAssertNormAnalyzer(t *testing.T) {
	t.Serial()
	dir, err := filepath.Abs("testdata/src/assertnorm")
	require.Nil(t, err)
	analysistest.Run(t, dir, AssertNormAnalyzer, ".")
}

func TestDeadCodeAnalyzer(t *testing.T) {
	t.Serial()
	testdata, err := filepath.Abs("testdata")
	require.Nil(t, err)
	analysistest.Run(t, testdata, DeadCodeAnalyzer, "deadcode")
}

func TestAnalyzers(t *testing.T) {
	t.Serial()
	analyzers := Analyzers()
	assert.NotEmpty(t, analyzers)

	names := set.New[string]()
	for _, a := range analyzers {
		names.Add(a.Name)
	}
	assert.True(t, names.Contains("assertlint"))
	assert.True(t, names.Contains("assertnorm"))
	assert.True(t, names.Contains("deadcode"))
	assert.True(t, names.Contains("redundantcast"))
	assert.True(t, names.Contains("testifycast"))
}

func TestRunNoGoMod(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	t.Chdir(dir)

	_, err := Run(false)
	assert.Nil(t, err)
}

// The retry exists so no importer stands between the type-check and a
// dependency, so NeedDeps is the whole point of it -- and the flag has to come
// back off, or every later pass pays for a source-loaded stdlib.
func TestLoadModeFromSource(t *testing.T) {
	t.Serial()
	assert.Zero(t, loadMode()&packages.NeedDeps, "the default reads export data")

	dir := t.TempDir()
	t.Chdir(dir)

	seen := packages.LoadMode(0)
	// No go.mod here, so RunFromSource returns before loading; read the mode from inside it.
	func() {
		loadDepsFromSource = true
		defer func() { loadDepsFromSource = false }()
		seen = loadMode()
	}()
	assert.NotZero(t, seen&packages.NeedDeps)

	_, err := RunFromSource(false, nil)
	require.NoError(t, err)
	assert.Zero(t, loadMode()&packages.NeedDeps, "RunFromSource must restore the default")
}

func TestSourceLocationShortLoc(t *testing.T) {
	t.Serial()
	cwd, err := os.Getwd()
	require.NoError(t, err)
	absPath := "/some/path/file.go"
	loc := SourceLocation{File: absPath, Line: 42, Column: 10}
	short := loc.ShortLoc()

	// A drive-rooted cwd relates to nothing here, and ShortLoc then keeps what it was given.
	expected, err := filepath.Rel(cwd, absPath)
	if err != nil {
		expected = absPath
	}
	assert.Equal(t, expected+":42", short)
}

func TestRunOnPatternWithValidCode(t *testing.T) {
	t.Serial()
	dir := t.TempDir()

	// Create go.mod
	goMod := `module testmod

go 1.21
`
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644)

	// Create valid Go code
	code := `package main

func main() {
	println("hello")
}
`
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(code), 0644)

	t.Chdir(dir)

	_, err := RunOnPattern("./...", false, nil)
	assert.Nil(t, err)
}

func TestSourceLocationShortLocRelative(t *testing.T) {
	t.Serial()
	cwd, err := os.Getwd()
	require.NoError(t, err)
	loc := SourceLocation{File: filepath.Join(cwd, "subdir", "file.go"), Line: 10, Column: 5}
	short := loc.ShortLoc()
	assert.Equal(t, filepath.Join("subdir", "file.go")+":10", short)
}

func TestRedundantCastFixes(t *testing.T) {
	t.Serial()
	tests := []struct {
		name   string
		before string
		after  string
	}{
		{
			name:   "int literal",
			before: "package main\n\nfunc main() { x := int(0); _ = x }",
			after:  "package main\n\nfunc main()\t{ x := 0; _ = x }\n",
		},
		{
			name:   "float64 literal",
			before: "package main\n\nfunc main() { x := float64(1.5); _ = x }",
			after:  "package main\n\nfunc main()\t{ x := 1.5; _ = x }\n",
		},
		{
			name:   "string literal",
			before: `package main` + "\n\n" + `func main() { x := string("hello"); _ = x }`,
			after:  "package main\n\nfunc main()\t{ x := \"hello\"; _ = x }\n",
		},
		{
			name:   "rune literal",
			before: "package main\n\nfunc main() { x := rune('a'); _ = x }",
			after:  "package main\n\nfunc main()\t{ x := 'a'; _ = x }\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "test.go", tt.before, parser.ParseComments)
			require.Nil(t, err)

			var call *ast.CallExpr
			ast.Inspect(f, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok {
					if _, ok := c.Fun.(*ast.Ident); ok {
						call = c
						return false
					}
				}
				return true
			})
			require.NotNil(t, call)

			fixes := &ASTFixes{File: f, Fset: fset, Fixes: []ASTFix{{OldNode: call, NewNodes: []ast.Node{call.Args[0]}}}}

			var buf strings.Builder
			err = fixes.Fprint(&buf)
			assert.Nil(t, err)
			assert.Equal(t, tt.after, buf.String())
		})
	}
}

func TestASTFixesDeletion(t *testing.T) {
	t.Serial()
	before := `package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", before, parser.ParseComments)
	require.Nil(t, err)

	// Find the import spec to delete
	var imp *ast.ImportSpec
	for _, i := range f.Imports {
		imp = i
		break
	}
	require.NotNil(t, imp)

	fixes := &ASTFixes{File: f, Fset: fset, Fixes: []ASTFix{{OldNode: imp, NewNodes: nil}}}

	var buf strings.Builder
	err = fixes.Fprint(&buf)
	assert.Nil(t, err)
	// After deletion, the import should be gone
	assert.NotContains(t, buf.String(), `"fmt"`)
}

func TestASTFixesApplyToFile(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.go")

	before := `package main

func main() {
	x := int(0)
	_ = x
}
`
	after := `package main

func main() {
	x := 0
	_ = x
}
`
	os.WriteFile(testFile, []byte(before), 0644)

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, testFile, nil, parser.ParseComments)
	require.Nil(t, err)

	var call *ast.CallExpr
	ast.Inspect(f, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "int" {
				call = c
				return false
			}
		}
		return true
	})
	require.NotNil(t, call)

	fixes := &ASTFixes{File: f, Fset: fset, Fixes: []ASTFix{{OldNode: call, NewNodes: []ast.Node{call.Args[0]}}}}
	_, err = fixes.Apply(NewEditor(true))
	assert.Nil(t, err)

	content, _ := os.ReadFile(testFile)
	assert.Equal(t, after, string(content))
}

func TestASTFixesApplyEmpty(t *testing.T) {
	t.Serial()
	fixes := &ASTFixes{Fixes: nil}
	_, err := fixes.Apply(NewEditor(true))
	assert.Nil(t, err)
}

func TestPrintFixMultiline(t *testing.T) {
	t.Serial()
	// Test that multiline nodes get truncated in printFix output
	fset := token.NewFileSet()
	src := `package main

func foo() {
	if true {
		println("a")
		println("b")
	}
}
`
	f, _ := parser.ParseFile(fset, "test.go", src, 0)

	// Find the if statement (multiline)
	var ifStmt *ast.IfStmt
	ast.Inspect(f, func(n ast.Node) bool {
		if i, ok := n.(*ast.IfStmt); ok {
			ifStmt = i
			return false
		}
		return true
	})
	require.NotNil(t, ifStmt)

	fixes := &ASTFixes{File: f, Fset: fset, Fixes: []ASTFix{{OldNode: ifStmt, NewNodes: nil}}}

	// Just ensure it doesn't panic
	fixes.printFix(fixes.Fixes[0])
}

func TestSourceLocationShortLocAbsolute(t *testing.T) {
	t.Serial()
	cwd, err := os.Getwd()
	require.NoError(t, err)
	absPath := "/nonexistent/path/file.go"
	loc := SourceLocation{File: absPath, Line: 10}
	short := loc.ShortLoc()

	// A path outside cwd still relates to it, unless the host roots them differently.
	expected, err := filepath.Rel(cwd, absPath)
	if err != nil {
		expected = absPath
	}
	assert.Equal(t, expected+":10", short)
}

func TestASTFixesCommentNotInterleaved(t *testing.T) {
	t.Serial()
	// Regression test: comments above an if statement must not be interleaved
	// into the replacement assert call arguments.
	before := `package main

func TestFoo(t *testing.T) {
	t.Serial()
	hostname := ""
	// Hostname should be non-empty
	if hostname == "" {
		t.Error("hostname should not be empty")
	}
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", before, parser.ParseComments)
	require.Nil(t, err)

	// Find the if statement
	var ifStmt *ast.IfStmt
	ast.Inspect(f, func(n ast.Node) bool {
		if i, ok := n.(*ast.IfStmt); ok {
			ifStmt = i
			return false
		}
		return true
	})
	require.NotNil(t, ifStmt)

	// Build the replacement generateASTFix produces for: if hostname == "" { t.Error(...) }
	bin := ifStmt.Cond.(*ast.BinaryExpr)
	assertCall := makeCall(
		makeSelector("assert", "NotEqual"),
		ast.NewIdent("t"),
		bin.Y, // "" (reused from original, has stale position)
		bin.X, // hostname (reused from original, has stale position)
	)
	assertStmt := &ast.ExprStmt{X: assertCall}

	newNodes := []ast.Node{assertStmt}
	prepareFixNodes(newNodes, ifStmt.Pos())

	fixes := &ASTFixes{
		File: f, Fset: fset,
		Fixes: []ASTFix{{OldNode: ifStmt, NewNodes: newNodes}},
	}

	var buf strings.Builder
	err = fixes.Fprint(&buf)
	require.Nil(t, err)

	result := buf.String()
	// The comment must NOT appear inside the function call
	assert.NotContains(t, result, "NotEqual(t,// Hostname")
	assert.NotContains(t, result, "NotEqual(t, // Hostname")
	// The comment should be on its own line before the assertion
	assert.Contains(t, result, "// Hostname should be non-empty\n\tassert.NotEqual")
}
