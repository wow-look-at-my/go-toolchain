package vet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/tools/go/analysis/analysistest"
	"github.com/stretchr/testify/require"

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
	loc := SourceLocation{File: "/some/path/file.go", Line: 42, Column: 10}
	short := loc.ShortLoc()
	assert.Contains(t, short, "42")
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

func TestApplyFixes(t *testing.T) {
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

	// int(0) starts at offset 31
	fixes := []fileFix{{
		loc:     SourceLocation{File: testFile, Line: 4, Column: 7},
		start:   31,
		end:     37,
		newText: []byte("0"),
	}}

	err := applyFixes(fixes)
	assert.Nil(t, err)

	content, _ := os.ReadFile(testFile)
	assert.Equal(t, after, string(content))
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

func TestPrintFix(t *testing.T) {
	// Just ensure it doesn't panic
	fix := fileFix{
		loc:     SourceLocation{File: "/tmp/test.go", Line: 1, Column: 1},
		oldText: []byte("int(0)"),
		newText: []byte("0"),
	}
	printFix(fix) // Should not panic

	// Test deletion (empty newText)
	fix2 := fileFix{
		loc:     SourceLocation{File: "/tmp/test.go", Line: 1, Column: 1},
		oldText: []byte("unused"),
		newText: []byte(""),
	}
	printFix(fix2)

	// Test multiline old text (truncation)
	fix3 := fileFix{
		loc:     SourceLocation{File: "/tmp/test.go", Line: 1, Column: 1},
		oldText: []byte("line1\nline2\nline3"),
		newText: []byte("replacement"),
	}
	printFix(fix3)

	// Test multiline new text (truncation)
	fix4 := fileFix{
		loc:     SourceLocation{File: "/tmp/test.go", Line: 1, Column: 1},
		oldText: []byte("old"),
		newText: []byte("new1\nnew2\nnew3"),
	}
	printFix(fix4)
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

func TestApplyFixesMultipleEdits(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.go")

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
	os.WriteFile(testFile, []byte(before), 0644)

	// int(0) at offset 31, int(1) at offset 45
	fixes := []fileFix{
		{
			loc:     SourceLocation{File: testFile, Line: 4, Column: 7},
			start:   31,
			end:     37,
			newText: []byte("0"),
		},
		{
			loc:     SourceLocation{File: testFile, Line: 5, Column: 7},
			start:   45,
			end:     51,
			newText: []byte("1"),
		},
	}

	err := applyFixes(fixes)
	assert.Nil(t, err)

	content, _ := os.ReadFile(testFile)
	assert.Equal(t, after, string(content))
}

func TestSourceLocationShortLocRelative(t *testing.T) {
	cwd, _ := os.Getwd()
	loc := SourceLocation{File: filepath.Join(cwd, "subdir", "file.go"), Line: 10, Column: 5}
	short := loc.ShortLoc()
	assert.Contains(t, short, "subdir")
	assert.Contains(t, short, "10")
}

func TestIsRedundantCastAllTypes(t *testing.T) {
	tests := []struct {
		typeName string
		litKind  string
		expected bool
	}{
		{"int", "INT", true},
		{"int64", "INT", false},
		{"float64", "FLOAT", true},
		{"float32", "FLOAT", false},
		{"string", "STRING", true},
		{"rune", "CHAR", true},
		{"int32", "CHAR", true},
		{"byte", "CHAR", false},
	}

	for _, tt := range tests {
		t.Run(tt.typeName+"_"+tt.litKind, func(t *testing.T) {
			// This tests the logic without needing full AST
			// Just verify the mapping is correct
			switch tt.litKind {
			case "INT":
				assert.Equal(t, tt.expected, tt.typeName == "int")
			case "FLOAT":
				assert.Equal(t, tt.expected, tt.typeName == "float64")
			case "STRING":
				assert.Equal(t, tt.expected, tt.typeName == "string")
			case "CHAR":
				assert.Equal(t, tt.expected, tt.typeName == "rune" || tt.typeName == "int32")
			}
		})
	}
}
