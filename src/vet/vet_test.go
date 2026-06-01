package vet

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"

	"path/filepath"
	"strings"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
	"golang.org/x/tools/go/analysis/analysistest"
)

func TestRedundantCastAnalyzer(t *testing.T) {
	testdata, err := filepath.Abs("testdata")
	require.Nil(t, err)
	analysistest.Run(t, testdata, RedundantCastAnalyzer, "redundantcast")
}

func TestAssertLintAnalyzer(t *testing.T) {
	testdata, err := filepath.Abs("testdata")
	require.Nil(t, err)
	analysistest.Run(t, testdata, AssertLintAnalyzer, "assertlint")
}

func TestAssertNormAnalyzer(t *testing.T) {
	dir, err := filepath.Abs("testdata/src/assertnorm")
	require.Nil(t, err)
	analysistest.Run(t, dir, AssertNormAnalyzer, ".")
}

func TestAnalyzers(t *testing.T) {
	analyzers := Analyzers()
	assert.NotEmpty(t, analyzers)

	names := make(map[string]bool)
	for _, a := range analyzers {
		names[a.Name] = true
	}
	assert.True(t, names["assertlint"])
	assert.True(t, names["assertnorm"])
	assert.True(t, names["redundantcast"])
	assert.True(t, names["testifycast"])
}

func TestRunNoGoMod(t *testing.T) {
	dir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	_, err := Run(false)
	assert.Nil(t, err)
}

func TestSourceLocationShortLoc(t *testing.T) {
	// filepath.Rel will make any absolute path relative to cwd
	cwd, _ := os.Getwd()
	absPath := "/some/path/file.go"
	loc := SourceLocation{File: absPath, Line: 42, Column: 10}
	short := loc.ShortLoc()

	expected, _ := filepath.Rel(cwd, absPath)
	assert.Equal(t, expected+":42", short)
}

func TestRunOnPatternWithValidCode(t *testing.T) {
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

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	_, err := RunOnPattern("./...", false, nil)
	assert.Nil(t, err)
}

func TestASTFixesFprint(t *testing.T) {
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

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", before, parser.ParseComments)
	require.Nil(t, err)

	// Find int(0) call
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

	var buf strings.Builder
	err = fixes.Fprint(&buf)
	assert.Nil(t, err)
	assert.Equal(t, after, buf.String())
}

func TestASTFixesFprintMultiple(t *testing.T) {
	before := `package main

func main() {
	x := int(0)
	y := int(1)
	_ = x
	_ = y
}
`
	after := `package main

func main() {
	x := 0
	y := 1
	_ = x
	_ = y
}
`

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", before, parser.ParseComments)
	require.Nil(t, err)

	var fixes []ASTFix
	ast.Inspect(f, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "int" {
				fixes = append(fixes, ASTFix{OldNode: c, NewNodes: []ast.Node{c.Args[0]}})
			}
		}
		return true
	})
	require.Len(t, fixes, 2)

	astFixes := &ASTFixes{File: f, Fset: fset, Fixes: fixes}

	var buf strings.Builder
	err = astFixes.Fprint(&buf)
	assert.Nil(t, err)
	assert.Equal(t, after, buf.String())
}

func TestASTFixesPrintFix(t *testing.T) {
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "test.go", `package main; func main() { x := int(0); _ = x }`, 0)

	var call *ast.CallExpr
	ast.Inspect(f, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			call = c
			return false
		}
		return true
	})

	fixes := &ASTFixes{File: f, Fset: fset, Fixes: []ASTFix{
		{OldNode: call, NewNodes: []ast.Node{call.Args[0]}},	// replacement
		{OldNode: call, NewNodes: nil},				// deletion
	}}

	// Just ensure printFix doesn't panic
	for _, fix := range fixes.Fixes {
		fixes.printFix(fix)
	}
}

func TestSourceLocationShortLocRelative(t *testing.T) {
	cwd, _ := os.Getwd()
	loc := SourceLocation{File: filepath.Join(cwd, "subdir", "file.go"), Line: 10, Column: 5}
	short := loc.ShortLoc()
	assert.Equal(t, "subdir/file.go:10", short)
}

func TestRedundantCastFixes(t *testing.T) {
	tests := []struct {
		name	string
		before	string
		after	string
	}{
		{
			name:	"int literal",
			before:	"package main\n\nfunc main() { x := int(0); _ = x }",
			after:	"package main\n\nfunc main()\t{ x := 0; _ = x }\n",
		},
		{
			name:	"float64 literal",
			before:	"package main\n\nfunc main() { x := float64(1.5); _ = x }",
			after:	"package main\n\nfunc main()\t{ x := 1.5; _ = x }\n",
		},
		{
			name:	"string literal",
			before:	`package main` + "\n\n" + `func main() { x := string("hello"); _ = x }`,
			after:	"package main\n\nfunc main()\t{ x := \"hello\"; _ = x }\n",
		},
		{
			name:	"rune literal",
			before:	"package main\n\nfunc main() { x := rune('a'); _ = x }",
			after:	"package main\n\nfunc main()\t{ x := 'a'; _ = x }\n",
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
	err = fixes.Apply()
	assert.Nil(t, err)

	content, _ := os.ReadFile(testFile)
	assert.Equal(t, after, string(content))
}

func TestASTFixesApplyEmpty(t *testing.T) {
	fixes := &ASTFixes{Fixes: nil}
	err := fixes.Apply()
	assert.Nil(t, err)
}

func TestPrintFixMultiline(t *testing.T) {
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
	// filepath.Rel will return a relative path even for paths outside cwd
	cwd, _ := os.Getwd()
	absPath := "/nonexistent/path/file.go"
	loc := SourceLocation{File: absPath, Line: 10}
	short := loc.ShortLoc()

	// The result should end with file.go:10
	expected, _ := filepath.Rel(cwd, absPath)
	assert.Equal(t, expected+":10", short)
}

func TestASTFixesCommentNotInterleaved(t *testing.T) {
	// Regression test: comments above an if statement must not be interleaved
	// into the replacement assert call arguments.
	before := `package main

func TestFoo(t *testing.T) {
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

	// Build replacement: assert.NotEqual(t, "", hostname)
	// This simulates what generateASTFix produces for: if hostname == "" { t.Error(...) }
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
