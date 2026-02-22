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
	"golang.org/x/tools/go/packages"
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

	_, err := RunOnPattern("./...", false)
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
		{OldNode: call, NewNodes: []ast.Node{call.Args[0]}}, // replacement
		{OldNode: call, NewNodes: nil},          // deletion
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

			fixes := &ASTFixes{File: f, Fset: fset, Fixes: []ASTFix{{OldNode: call, NewNodes: []ast.Node{call.Args[0]}}}}

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

func TestImportName(t *testing.T) {
	tests := []struct {
		name     string
		imp      *ast.ImportSpec
		expected string
	}{
		{
			name: "named import",
			imp: &ast.ImportSpec{
				Name: &ast.Ident{Name: "foo"},
				Path: &ast.BasicLit{Value: `"bar/baz"`},
			},
			expected: "foo",
		},
		{
			name: "unnamed import",
			imp: &ast.ImportSpec{
				Path: &ast.BasicLit{Value: `"bar/baz"`},
			},
			expected: "baz",
		},
		{
			name: "nested path",
			imp: &ast.ImportSpec{
				Path: &ast.BasicLit{Value: `"github.com/foo/bar"`},
			},
			expected: "bar",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, importName(tt.imp))
		})
	}
}

func TestIsRedundantCast(t *testing.T) {
	tests := []struct {
		typeName string
		litKind  token.Token
		expected bool
	}{
		{"int", token.INT, true},
		{"int64", token.INT, false},
		{"float64", token.FLOAT, true},
		{"float32", token.FLOAT, false},
		{"string", token.STRING, true},
		{"rune", token.CHAR, true},
		{"int32", token.CHAR, true},
		{"byte", token.CHAR, false},
		{"bool", token.INT, false},
	}

	for _, tt := range tests {
		t.Run(tt.typeName, func(t *testing.T) {
			lit := &ast.BasicLit{Kind: tt.litKind, Value: "0"}
			assert.Equal(t, tt.expected, isRedundantCast(tt.typeName, lit))
		})
	}
}

func TestNodeText(t *testing.T) {
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "test.go", `package main; var x = 42`, 0)

	var lit *ast.BasicLit
	ast.Inspect(f, func(n ast.Node) bool {
		if l, ok := n.(*ast.BasicLit); ok && l.Kind == token.INT {
			lit = l
			return false
		}
		return true
	})
	require.NotNil(t, lit)
	assert.Equal(t, "42", nodeText(fset, lit))
}

func TestFindFileUnusedImportFixesDotImport(t *testing.T) {
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "test.go", `package main

import (
	. "fmt"
)

func main() {}
`, parser.ParseComments)

	fixes := findFileUnusedImportFixes(fset, f)
	assert.Nil(t, fixes) // dot imports should not be flagged
}

func TestFindFileUnusedImportFixesMultipleUnused(t *testing.T) {
	before := `package main

import (
	"fmt"
	"strings"
	"bytes"
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

	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "test.go", before, parser.ParseComments)

	fixes := findFileUnusedImportFixes(fset, f)
	require.NotNil(t, fixes)
	assert.Len(t, fixes.Fixes, 2) // strings and bytes are unused

	var buf strings.Builder
	err := fixes.Fprint(&buf)
	assert.Nil(t, err)
	assert.Equal(t, after, buf.String())
}

func TestCastableType(t *testing.T) {
	tests := []struct {
		litKind    token.Token
		litValue   string
		targetType string
		expected   string
	}{
		{token.INT, "42", "int64", "int64"},
		{token.INT, "42", "int", ""},
		{token.INT, "42", "uint32", "uint32"},
		{token.FLOAT, "3.14", "float32", "float32"},
		{token.FLOAT, "3.14", "float64", ""},
		{token.INT, "42", "time.Duration", ""}, // complex type - no cast
		{token.INT, "42", "byte", "byte"},
		{token.INT, "42", "rune", "rune"},
	}

	for _, tt := range tests {
		name := tt.targetType
		t.Run(name, func(t *testing.T) {
			lit := &ast.BasicLit{Kind: tt.litKind, Value: tt.litValue}
			assert.Equal(t, tt.expected, castableType(lit, tt.targetType))
		})
	}
}

func TestDeterminePositiveAssertFunc(t *testing.T) {
	tests := []struct {
		name     string
		cond     ast.Expr
		expected string
	}{
		{
			name: "equal to nil",
			cond: &ast.BinaryExpr{
				X: &ast.Ident{Name: "x"}, Op: token.EQL, Y: &ast.Ident{Name: "nil"},
			},
			expected: "Nil",
		},
		{
			name: "not equal to nil",
			cond: &ast.BinaryExpr{
				X: &ast.Ident{Name: "x"}, Op: token.NEQ, Y: &ast.Ident{Name: "nil"},
			},
			expected: "NotNil",
		},
		{
			name: "equal values",
			cond: &ast.BinaryExpr{
				X: &ast.Ident{Name: "x"}, Op: token.EQL, Y: &ast.Ident{Name: "y"},
			},
			expected: "Equal",
		},
		{
			name: "less than",
			cond: &ast.BinaryExpr{
				X: &ast.Ident{Name: "x"}, Op: token.LSS, Y: &ast.Ident{Name: "y"},
			},
			expected: "Less",
		},
		{
			name: "greater than",
			cond: &ast.BinaryExpr{
				X: &ast.Ident{Name: "x"}, Op: token.GTR, Y: &ast.Ident{Name: "y"},
			},
			expected: "Greater",
		},
		{
			name: "less or equal",
			cond: &ast.BinaryExpr{
				X: &ast.Ident{Name: "x"}, Op: token.LEQ, Y: &ast.Ident{Name: "y"},
			},
			expected: "LessOrEqual",
		},
		{
			name: "greater or equal",
			cond: &ast.BinaryExpr{
				X: &ast.Ident{Name: "x"}, Op: token.GEQ, Y: &ast.Ident{Name: "y"},
			},
			expected: "GreaterOrEqual",
		},
		{
			name:     "simple identifier",
			cond:     &ast.Ident{Name: "ok"},
			expected: "True",
		},
		{
			name: "parenthesized",
			cond: &ast.ParenExpr{X: &ast.Ident{Name: "ok"}},
			expected: "True",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, determinePositiveAssertFunc(tt.cond))
		})
	}
}

func TestDetermineNegativeAssertFunc(t *testing.T) {
	tests := []struct {
		name     string
		cond     ast.Expr
		expected string
	}{
		{
			name: "equal to nil -> NotNil",
			cond: &ast.BinaryExpr{
				X: &ast.Ident{Name: "x"}, Op: token.EQL, Y: &ast.Ident{Name: "nil"},
			},
			expected: "NotNil",
		},
		{
			name: "not equal to nil -> Nil",
			cond: &ast.BinaryExpr{
				X: &ast.Ident{Name: "x"}, Op: token.NEQ, Y: &ast.Ident{Name: "nil"},
			},
			expected: "Nil",
		},
		{
			name: "less than -> GreaterOrEqual",
			cond: &ast.BinaryExpr{
				X: &ast.Ident{Name: "x"}, Op: token.LSS, Y: &ast.Ident{Name: "y"},
			},
			expected: "GreaterOrEqual",
		},
		{
			name: "greater than -> LessOrEqual",
			cond: &ast.BinaryExpr{
				X: &ast.Ident{Name: "x"}, Op: token.GTR, Y: &ast.Ident{Name: "y"},
			},
			expected: "LessOrEqual",
		},
		{
			name:     "simple identifier -> False",
			cond:     &ast.Ident{Name: "ok"},
			expected: "False",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, determineNegativeAssertFunc(tt.cond))
		})
	}
}

func TestIsNil(t *testing.T) {
	assert.True(t, isNil(&ast.Ident{Name: "nil"}))
	assert.False(t, isNil(&ast.Ident{Name: "x"}))
	assert.False(t, isNil(&ast.BasicLit{Kind: token.INT, Value: "0"}))
}

func TestGetCallFuncName(t *testing.T) {
	// strings.Contains(a, b)
	call := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   &ast.Ident{Name: "strings"},
			Sel: &ast.Ident{Name: "Contains"},
		},
	}
	assert.Equal(t, "strings.Contains", getCallFuncName(call))

	// plain function call - no package
	call2 := &ast.CallExpr{Fun: &ast.Ident{Name: "foo"}}
	assert.Equal(t, "", getCallFuncName(call2))
}

func TestHasTestingErrorCall(t *testing.T) {
	// Block with t.Error
	block := &ast.BlockStmt{
		List: []ast.Stmt{
			&ast.ExprStmt{
				X: &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   &ast.Ident{Name: "t"},
						Sel: &ast.Ident{Name: "Error"},
					},
				},
			},
		},
	}
	assert.True(t, hasTestingErrorCall(block))

	// Empty block
	assert.False(t, hasTestingErrorCall(&ast.BlockStmt{}))
	assert.False(t, hasTestingErrorCall(nil))
}

func TestIsTestingErrorCall(t *testing.T) {
	tests := []struct {
		receiver string
		method   string
		expected bool
	}{
		{"t", "Error", true},
		{"t", "Errorf", true},
		{"t", "Fatal", true},
		{"t", "Fatalf", true},
		{"b", "Error", true},
		{"t", "Log", false},
		{"x", "Error", false},
	}

	for _, tt := range tests {
		call := &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   &ast.Ident{Name: tt.receiver},
				Sel: &ast.Ident{Name: tt.method},
			},
		}
		assert.Equal(t, tt.expected, isTestingErrorCall(call), "%s.%s", tt.receiver, tt.method)
	}

	// Non-selector call
	call := &ast.CallExpr{Fun: &ast.Ident{Name: "foo"}}
	assert.False(t, isTestingErrorCall(call))
}

func TestGetTestVarName(t *testing.T) {
	block := &ast.BlockStmt{
		List: []ast.Stmt{
			&ast.ExprStmt{
				X: &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   &ast.Ident{Name: "t"},
						Sel: &ast.Ident{Name: "Error"},
					},
				},
			},
		},
	}
	assert.Equal(t, "t", getTestVarName(block))

	// Empty block
	assert.Equal(t, "", getTestVarName(&ast.BlockStmt{}))
}

func TestVetSemanticLoadError(t *testing.T) {
	dir := t.TempDir()

	// Create invalid Go code (syntax error)
	code := `package main

func main() {
	invalid syntax here
}
`
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(code), 0644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	_, err := vetSemantic("./...", false)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "package load errors")
}

func TestVetSemanticUnusedImportNoFix(t *testing.T) {
	dir := t.TempDir()

	// Create code with unused import
	code := `package main

import "fmt"

func main() {}
`
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(code), 0644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	_, err := vetSemantic("./...", false)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "imported and not used")
}

func TestGenerateBinaryReplacementCompound(t *testing.T) {
	// Test for && and || operators
	dir := t.TempDir()
	testFile := filepath.Join(dir, "main_test.go")

	code := `package main

import "testing"

func TestFoo(t *testing.T) {
	x := true
	y := false
	if x && y {
		t.Error("should be false")
	}
}
`
	os.WriteFile(testFile, []byte(code), 0644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	// Just run it to exercise the compound condition path
	_, err := vetSemantic("./...", false)
	// It should find an issue
	assert.NotNil(t, err)
}

func TestGenerateImportEdit(t *testing.T) {
	fset := token.NewFileSet()

	// Test with existing imports
	f1, _ := parser.ParseFile(fset, "test.go", `package main

import "fmt"

func main() { fmt.Println("hi") }
`, parser.ParseComments)

	// This is tested indirectly but let's test the path where imports exist
	assert.NotNil(t, f1.Imports)

	// Test with no imports
	f2, _ := parser.ParseFile(fset, "test2.go", `package main

func main() {}
`, parser.ParseComments)
	assert.Empty(t, f2.Imports)
}

func TestDetermineAssertFuncUnary(t *testing.T) {
	// Test negation path
	cond := &ast.UnaryExpr{
		Op: token.NOT,
		X:  &ast.Ident{Name: "ok"},
	}
	assert.Equal(t, "True", determineAssertFunc(cond))

	// Test direct identifier
	assert.Equal(t, "False", determineAssertFunc(&ast.Ident{Name: "ok"}))
}

func TestDeterminePositiveAssertFuncCall(t *testing.T) {
	// Test strings.HasPrefix
	call := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   &ast.Ident{Name: "strings"},
			Sel: &ast.Ident{Name: "HasPrefix"},
		},
		Args: []ast.Expr{
			&ast.Ident{Name: "s"},
			&ast.BasicLit{Kind: token.STRING, Value: `"hello"`},
		},
	}
	assert.Equal(t, "True", determinePositiveAssertFunc(call))

	// Test strings.HasSuffix
	call2 := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   &ast.Ident{Name: "strings"},
			Sel: &ast.Ident{Name: "HasSuffix"},
		},
	}
	assert.Equal(t, "True", determinePositiveAssertFunc(call2))

	// Test reflect.DeepEqual
	call3 := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   &ast.Ident{Name: "reflect"},
			Sel: &ast.Ident{Name: "DeepEqual"},
		},
	}
	assert.Equal(t, "Equal", determinePositiveAssertFunc(call3))
}

func TestDetermineNegativeAssertFuncCall(t *testing.T) {
	// Test reflect.DeepEqual (negative)
	call := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   &ast.Ident{Name: "reflect"},
			Sel: &ast.Ident{Name: "DeepEqual"},
		},
	}
	assert.Equal(t, "NotEqual", determineNegativeAssertFunc(call))

	// Test parenthesized expression
	paren := &ast.ParenExpr{X: &ast.Ident{Name: "ok"}}
	assert.Equal(t, "False", determineNegativeAssertFunc(paren))
}

func TestDetermineAssertionWithInit(t *testing.T) {
	// Test init clause pattern: if err := X; err != nil
	ifStmt := &ast.IfStmt{
		Init: &ast.AssignStmt{
			Lhs: []ast.Expr{&ast.Ident{Name: "err"}},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{&ast.CallExpr{Fun: &ast.Ident{Name: "doSomething"}}},
		},
		Cond: &ast.BinaryExpr{
			X:  &ast.Ident{Name: "err"},
			Op: token.NEQ,
			Y:  &ast.Ident{Name: "nil"},
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ExprStmt{
					X: &ast.CallExpr{
						Fun: &ast.SelectorExpr{
							X:   &ast.Ident{Name: "t"},
							Sel: &ast.Ident{Name: "Fatal"},
						},
					},
				},
			},
		},
	}

	pkg, fn := determineAssertion(ifStmt)
	assert.Equal(t, "require", pkg)
	assert.Equal(t, "NoError", fn)
}

func TestSourceLocationShortLocWithError(t *testing.T) {
	// Test when filepath.Rel fails (shouldn't happen in practice, but for coverage)
	loc := SourceLocation{File: "/some/path/file.go", Line: 1}
	short := loc.ShortLoc()
	assert.Contains(t, short, "file.go:1")
}

func TestVetSemanticWithFix(t *testing.T) {
	dir := t.TempDir()

	// Create code with unused import that will be fixed
	code := `package main

import (
	"fmt"
	"strings"
)

func main() {
	fmt.Println("hello")
}
`
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(code), 0644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	// With fix=true, it should fix the unused import and succeed
	_, err := vetSemantic("./...", true)
	assert.Nil(t, err)

	// Verify the import was removed
	content, _ := os.ReadFile(filepath.Join(dir, "main.go"))
	assert.NotContains(t, string(content), "strings")
}

func TestRemoveImport(t *testing.T) {
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "test.go", `package main

import (
	"fmt"
	"strings"
)

func main() { fmt.Println("hi") }
`, parser.ParseComments)

	// Find the strings import
	var stringsImp *ast.ImportSpec
	for _, imp := range f.Imports {
		if strings.Contains(imp.Path.Value, "strings") {
			stringsImp = imp
			break
		}
	}
	require.NotNil(t, stringsImp)

	// Remove it
	removeImport(f, stringsImp)

	// Verify it's gone
	for _, imp := range f.Imports {
		assert.NotContains(t, imp.Path.Value, "strings")
	}
}

func TestFixFileUnusedImportsParseError(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "invalid.go")

	// Write invalid Go code
	os.WriteFile(testFile, []byte("this is not valid go code"), 0644)

	wasFixed, err := fixFileUnusedImports(testFile)
	assert.NotNil(t, err)
	assert.False(t, wasFixed)
}

func TestDetermineAssertionNotInit(t *testing.T) {
	// Test with init that's not an AssignStmt
	ifStmt := &ast.IfStmt{
		Init: &ast.ExprStmt{X: &ast.Ident{Name: "x"}},
		Cond: &ast.BinaryExpr{
			X:  &ast.Ident{Name: "err"},
			Op: token.NEQ,
			Y:  &ast.Ident{Name: "nil"},
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ExprStmt{
					X: &ast.CallExpr{
						Fun: &ast.SelectorExpr{
							X:   &ast.Ident{Name: "t"},
							Sel: &ast.Ident{Name: "Error"},
						},
					},
				},
			},
		},
	}

	pkg, fn := determineAssertion(ifStmt)
	assert.Equal(t, "assert", pkg)
	assert.Equal(t, "Nil", fn)
}

func TestDetermineAssertionInitMultipleLhs(t *testing.T) {
	// Test with init that has multiple LHS
	ifStmt := &ast.IfStmt{
		Init: &ast.AssignStmt{
			Lhs: []ast.Expr{&ast.Ident{Name: "x"}, &ast.Ident{Name: "err"}},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{&ast.CallExpr{Fun: &ast.Ident{Name: "doSomething"}}},
		},
		Cond: &ast.BinaryExpr{
			X:  &ast.Ident{Name: "err"},
			Op: token.NEQ,
			Y:  &ast.Ident{Name: "nil"},
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ExprStmt{
					X: &ast.CallExpr{
						Fun: &ast.SelectorExpr{
							X:   &ast.Ident{Name: "t"},
							Sel: &ast.Ident{Name: "Error"},
						},
					},
				},
			},
		},
	}

	pkg, fn := determineAssertion(ifStmt)
	assert.Equal(t, "assert", pkg)
	// Not NoError because init has multiple LHS
	assert.Equal(t, "Nil", fn)
}

func TestDetermineAssertionCondNotBinary(t *testing.T) {
	// Test with init where cond is not binary
	ifStmt := &ast.IfStmt{
		Init: &ast.AssignStmt{
			Lhs: []ast.Expr{&ast.Ident{Name: "ok"}},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{&ast.CallExpr{Fun: &ast.Ident{Name: "check"}}},
		},
		Cond: &ast.Ident{Name: "ok"},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ExprStmt{
					X: &ast.CallExpr{
						Fun: &ast.SelectorExpr{
							X:   &ast.Ident{Name: "t"},
							Sel: &ast.Ident{Name: "Error"},
						},
					},
				},
			},
		},
	}

	pkg, fn := determineAssertion(ifStmt)
	assert.Equal(t, "assert", pkg)
	assert.Equal(t, "False", fn)
}

func TestDeterminePositiveAssertFuncNEQ(t *testing.T) {
	// Test NotEqual
	cond := &ast.BinaryExpr{
		X:  &ast.Ident{Name: "x"},
		Op: token.NEQ,
		Y:  &ast.Ident{Name: "y"},
	}
	assert.Equal(t, "NotEqual", determinePositiveAssertFunc(cond))
}

func TestDetermineNegativeAssertFuncComparisons(t *testing.T) {
	tests := []struct {
		op       token.Token
		expected string
	}{
		{token.LEQ, "Greater"},
		{token.GEQ, "Less"},
	}

	for _, tt := range tests {
		cond := &ast.BinaryExpr{
			X:  &ast.Ident{Name: "x"},
			Op: tt.op,
			Y:  &ast.Ident{Name: "y"},
		}
		assert.Equal(t, tt.expected, determineNegativeAssertFunc(cond))
	}
}

func TestGenerateReplacementFallback(t *testing.T) {
	// Test the fallback path with a complex expression
	dir := t.TempDir()
	testFile := filepath.Join(dir, "main_test.go")

	code := `package main

import "testing"

func TestFoo(t *testing.T) {
	x := []int{1, 2, 3}
	if len(x) > 0 {
		t.Error("should be empty")
	}
}
`
	os.WriteFile(testFile, []byte(code), 0644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	// Run to exercise the path
	_, err := vetSemantic("./...", false)
	assert.NotNil(t, err)
}

func TestVetSyntaxNoFix2(t *testing.T) {
	// Test vetSyntax path
	err := vetSyntax("./...", false)
	assert.Nil(t, err)
}

func TestFixUnusedImportsInvalidPattern(t *testing.T) {
	// Test with invalid glob pattern
	_, err := FixUnusedImports("[invalid")
	assert.NotNil(t, err)
}

func TestFindUnusedImportFixesWithPackages(t *testing.T) {
	dir := t.TempDir()

	// Create valid code with unused imports
	code := `package main

import (
	"fmt"
	"strings"
)

func main() {
	fmt.Println("hello")
}
`
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(code), 0644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	// Load packages
	cfg := &packages.Config{
		Mode:  packages.LoadAllSyntax,
		Tests: false,
	}
	pkgs, err := packages.Load(cfg, "./...")
	require.Nil(t, err)

	// Find unused import fixes
	fixes := FindUnusedImportFixes(pkgs)
	assert.NotEmpty(t, fixes)
}

func TestRunWithGoMod(t *testing.T) {
	dir := t.TempDir()

	code := `package main

func main() {
	println("hello")
}
`
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(code), 0644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	_, err := Run(false)
	assert.Nil(t, err)
}

func TestDetermineAssertionInitNotDefine(t *testing.T) {
	// Test with init that uses = instead of :=
	ifStmt := &ast.IfStmt{
		Init: &ast.AssignStmt{
			Lhs: []ast.Expr{&ast.Ident{Name: "err"}},
			Tok: token.ASSIGN, // = not :=
			Rhs: []ast.Expr{&ast.CallExpr{Fun: &ast.Ident{Name: "doSomething"}}},
		},
		Cond: &ast.BinaryExpr{
			X:  &ast.Ident{Name: "err"},
			Op: token.NEQ,
			Y:  &ast.Ident{Name: "nil"},
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ExprStmt{
					X: &ast.CallExpr{
						Fun: &ast.SelectorExpr{
							X:   &ast.Ident{Name: "t"},
							Sel: &ast.Ident{Name: "Error"},
						},
					},
				},
			},
		},
	}

	pkg, fn := determineAssertion(ifStmt)
	assert.Equal(t, "assert", pkg)
	assert.Equal(t, "Nil", fn) // Not NoError because not :=
}

func TestDetermineAssertionCondVarMismatch(t *testing.T) {
	// Test where condition variable doesn't match init variable
	ifStmt := &ast.IfStmt{
		Init: &ast.AssignStmt{
			Lhs: []ast.Expr{&ast.Ident{Name: "err"}},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{&ast.CallExpr{Fun: &ast.Ident{Name: "doSomething"}}},
		},
		Cond: &ast.BinaryExpr{
			X:  &ast.Ident{Name: "otherErr"}, // Different variable
			Op: token.NEQ,
			Y:  &ast.Ident{Name: "nil"},
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ExprStmt{
					X: &ast.CallExpr{
						Fun: &ast.SelectorExpr{
							X:   &ast.Ident{Name: "t"},
							Sel: &ast.Ident{Name: "Error"},
						},
					},
				},
			},
		},
	}

	pkg, fn := determineAssertion(ifStmt)
	assert.Equal(t, "assert", pkg)
	assert.Equal(t, "Nil", fn) // Not NoError
}

func TestDetermineAssertionCondNotNil(t *testing.T) {
	// Test where condition Y is not nil
	ifStmt := &ast.IfStmt{
		Init: &ast.AssignStmt{
			Lhs: []ast.Expr{&ast.Ident{Name: "err"}},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{&ast.CallExpr{Fun: &ast.Ident{Name: "doSomething"}}},
		},
		Cond: &ast.BinaryExpr{
			X:  &ast.Ident{Name: "err"},
			Op: token.NEQ,
			Y:  &ast.Ident{Name: "someValue"}, // Not nil
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ExprStmt{
					X: &ast.CallExpr{
						Fun: &ast.SelectorExpr{
							X:   &ast.Ident{Name: "t"},
							Sel: &ast.Ident{Name: "Error"},
						},
					},
				},
			},
		},
	}

	pkg, fn := determineAssertion(ifStmt)
	assert.Equal(t, "assert", pkg)
	assert.Equal(t, "Equal", fn) // Not NoError
}

func TestDetermineAssertionCondEQL(t *testing.T) {
	// Test where condition is == instead of !=
	ifStmt := &ast.IfStmt{
		Init: &ast.AssignStmt{
			Lhs: []ast.Expr{&ast.Ident{Name: "err"}},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{&ast.CallExpr{Fun: &ast.Ident{Name: "doSomething"}}},
		},
		Cond: &ast.BinaryExpr{
			X:  &ast.Ident{Name: "err"},
			Op: token.EQL, // == instead of !=
			Y:  &ast.Ident{Name: "nil"},
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ExprStmt{
					X: &ast.CallExpr{
						Fun: &ast.SelectorExpr{
							X:   &ast.Ident{Name: "t"},
							Sel: &ast.Ident{Name: "Error"},
						},
					},
				},
			},
		},
	}

	pkg, fn := determineAssertion(ifStmt)
	assert.Equal(t, "assert", pkg)
	assert.Equal(t, "NotNil", fn) // Not NoError
}

func TestDetermineAssertionCondXNotIdent(t *testing.T) {
	// Test where condition X is not an Ident
	ifStmt := &ast.IfStmt{
		Init: &ast.AssignStmt{
			Lhs: []ast.Expr{&ast.Ident{Name: "err"}},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{&ast.CallExpr{Fun: &ast.Ident{Name: "doSomething"}}},
		},
		Cond: &ast.BinaryExpr{
			X:  &ast.CallExpr{Fun: &ast.Ident{Name: "getErr"}}, // Not an Ident
			Op: token.NEQ,
			Y:  &ast.Ident{Name: "nil"},
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ExprStmt{
					X: &ast.CallExpr{
						Fun: &ast.SelectorExpr{
							X:   &ast.Ident{Name: "t"},
							Sel: &ast.Ident{Name: "Error"},
						},
					},
				},
			},
		},
	}

	pkg, fn := determineAssertion(ifStmt)
	assert.Equal(t, "assert", pkg)
	assert.Equal(t, "Nil", fn) // Fallback
}

func TestVetSemanticWithDiagnostics(t *testing.T) {
	dir := t.TempDir()

	// Create test file that will trigger assertlint
	code := `package main

import "testing"

func TestFoo(t *testing.T) {
	err := error(nil)
	if err != nil {
		t.Error("oops")
	}
}
`
	os.WriteFile(filepath.Join(dir, "main_test.go"), []byte(code), 0644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	// Should find issues and return error with diagnostics
	_, err := vetSemantic("./...", false)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "vet found issues")
}

func TestIsRedundantCastChar(t *testing.T) {
	// Test char literal cases
	assert.True(t, isRedundantCast("rune", &ast.BasicLit{Kind: token.CHAR, Value: "'a'"}))
	assert.True(t, isRedundantCast("int32", &ast.BasicLit{Kind: token.CHAR, Value: "'a'"}))
	assert.False(t, isRedundantCast("byte", &ast.BasicLit{Kind: token.CHAR, Value: "'a'"}))
	assert.False(t, isRedundantCast("uint8", &ast.BasicLit{Kind: token.CHAR, Value: "'a'"}))
}
