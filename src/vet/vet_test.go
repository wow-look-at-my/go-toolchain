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

func TestAnalyzers(t *testing.T) {
	analyzers := Analyzers()
	assert.NotEmpty(t, analyzers)

	names := make(map[string]bool)
	for _, a := range analyzers {
		names[a.Name] = true
	}
	assert.True(t, names["assertlint"])
	assert.True(t, names["redundantcast"])
}

func TestRunNoGoMod(t *testing.T) {
	dir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	err := Run(false)
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

func TestIsUnusedImportError(t *testing.T) {
	assert.True(t, isUnusedImportError(`"fmt" imported and not used`))
	assert.False(t, isUnusedImportError("undefined: foo"))
}

func TestFixUnusedImports(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "main.go")

	before := `package main

import (
	"fmt"
	"strings"
)

func main() {
	fmt.Println("hello")
}
`
	after := `package main

import (
	"fmt"
)

func main() {
	fmt.Println("hello")
}
`
	os.WriteFile(testFile, []byte(before), 0644)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	fixed, err := FixUnusedImports("./...")
	assert.Nil(t, err)
	assert.Len(t, fixed, 1)

	content, _ := os.ReadFile(testFile)
	assert.Equal(t, after, string(content))
}

func TestFixUnusedImportsNoUnused(t *testing.T) {
	dir := t.TempDir()

	code := `package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`
	testFile := filepath.Join(dir, "main.go")
	os.WriteFile(testFile, []byte(code), 0644)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	fixed, err := FixUnusedImports("./...")
	assert.Nil(t, err)
	assert.Empty(t, fixed)
}

func TestFixUnusedImportsBlankAndDot(t *testing.T) {
	dir := t.TempDir()

	// Blank and dot imports should not be removed
	code := `package main

import (
	"fmt"
	_ "embed"
)

func main() {
	fmt.Println("hello")
}
`
	testFile := filepath.Join(dir, "main.go")
	os.WriteFile(testFile, []byte(code), 0644)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	fixed, err := FixUnusedImports("./...")
	assert.Nil(t, err)
	assert.Empty(t, fixed) // blank import should be kept
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

	err := RunOnPattern("./...", false)
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

	fixes := &ASTFixes{File: f, Fset: fset, Fixes: []ASTFix{{OldNode: call, NewNode: call.Args[0]}}}

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
				fixes = append(fixes, ASTFix{OldNode: c, NewNode: c.Args[0]})
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
		{OldNode: call, NewNode: call.Args[0]}, // replacement
		{OldNode: call, NewNode: nil},          // deletion
	}}

	// Just ensure printFix doesn't panic
	for _, fix := range fixes.Fixes {
		fixes.printFix(fix)
	}
}

func TestFixUnusedImportsGlobPattern(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "main.go")

	before := `package main

import (
	"fmt"
	"strings"
)

func main() {
	fmt.Println("hello")
}
`
	after := `package main

import (
	"fmt"
)

func main() {
	fmt.Println("hello")
}
`
	os.WriteFile(testFile, []byte(before), 0644)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	fixed, err := FixUnusedImports("*.go")
	assert.Nil(t, err)
	assert.Len(t, fixed, 1)

	content, _ := os.ReadFile(testFile)
	assert.Equal(t, after, string(content))
}

func TestFixUnusedImportsWithAlias(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "main.go")

	before := `package main

import (
	"fmt"
	s "strings"
)

func main() {
	fmt.Println("hello")
}
`
	after := `package main

import (
	"fmt"
)

func main() {
	fmt.Println("hello")
}
`
	os.WriteFile(testFile, []byte(before), 0644)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	fixed, err := FixUnusedImports("./...")
	assert.Nil(t, err)
	assert.Len(t, fixed, 1)

	content, _ := os.ReadFile(testFile)
	assert.Equal(t, after, string(content))
}

func TestVetSyntaxNoFix(t *testing.T) {
	// vetSyntax with fix=false should do nothing
	err := vetSyntax("./...", false)
	assert.Nil(t, err)
}

func TestVetSyntaxWithFix(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "main.go")

	before := `package main

import (
	"fmt"
	"strings"
)

func main() {
	fmt.Println("hello")
}
`
	after := `package main

import (
	"fmt"
)

func main() {
	fmt.Println("hello")
}
`
	os.WriteFile(testFile, []byte(before), 0644)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	err := vetSyntax("./...", true)
	assert.Nil(t, err)

	content, _ := os.ReadFile(testFile)
	assert.Equal(t, after, string(content))
}

func TestSourceLocationShortLocRelative(t *testing.T) {
	cwd, _ := os.Getwd()
	loc := SourceLocation{File: filepath.Join(cwd, "subdir", "file.go"), Line: 10, Column: 5}
	short := loc.ShortLoc()
	assert.Equal(t, "subdir/file.go:10", short)
}

func TestRedundantCastFixes(t *testing.T) {
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

			fixes := &ASTFixes{File: f, Fset: fset, Fixes: []ASTFix{{OldNode: call, NewNode: call.Args[0]}}}

			var buf strings.Builder
			err = fixes.Fprint(&buf)
			assert.Nil(t, err)
			assert.Equal(t, tt.after, buf.String())
		})
	}
}

func TestUnusedImportFixes(t *testing.T) {
	tests := []struct {
		name   string
		before string
		after  string
	}{
		{
			name: "single unused import",
			before: `package main

import (
	"fmt"
	"strings"
)

func main() {
	fmt.Println("hello")
}
`,
			after: `package main

import (
	"fmt"
)

func main() {
	fmt.Println("hello")
}
`,
		},
		{
			name: "aliased unused import",
			before: `package main

import (
	"fmt"
	s "strings"
)

func main() {
	fmt.Println("hello")
}
`,
			after: `package main

import (
	"fmt"
)

func main() {
	fmt.Println("hello")
}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			testFile := filepath.Join(dir, "main.go")
			os.WriteFile(testFile, []byte(tt.before), 0644)

			oldWd, _ := os.Getwd()
			os.Chdir(dir)
			defer os.Chdir(oldWd)

			_, err := FixUnusedImports("./...")
			assert.Nil(t, err)

			content, _ := os.ReadFile(testFile)
			assert.Equal(t, tt.after, string(content))
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

	fixes := &ASTFixes{File: f, Fset: fset, Fixes: []ASTFix{{OldNode: imp, NewNode: nil}}}

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

	fixes := &ASTFixes{File: f, Fset: fset, Fixes: []ASTFix{{OldNode: call, NewNode: call.Args[0]}}}
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

	fixes := &ASTFixes{File: f, Fset: fset, Fixes: []ASTFix{{OldNode: ifStmt, NewNode: nil}}}

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

func TestFindUnusedImportFixesBlankImport(t *testing.T) {
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "test.go", `package main

import (
	_ "embed"
)

func main() {}
`, parser.ParseComments)

	fixes := findFileUnusedImportFixes(fset, f)
	assert.Nil(t, fixes) // blank imports should not be flagged
}

func TestFindUnusedImportFixesNoImports(t *testing.T) {
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "test.go", `package main

func main() {}
`, parser.ParseComments)

	fixes := findFileUnusedImportFixes(fset, f)
	assert.Nil(t, fixes)
}
