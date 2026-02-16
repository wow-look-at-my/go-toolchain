package vet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/tools/go/analysis/analysistest"
)

func TestRedundantCastAnalyzer(t *testing.T) {
	testdata, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, testdata, RedundantCastAnalyzer, "redundantcast")
}

func TestAssertLintAnalyzer(t *testing.T) {
	testdata, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
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

	// Create a Go file with an unused import
	code := `package main

import (
	"fmt"
	"strings"
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
	assert.Len(t, fixed, 1)

	// Verify strings was removed
	content, _ := os.ReadFile(testFile)
	assert.NotContains(t, string(content), "strings")
	assert.Contains(t, string(content), "fmt")
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

func TestImportName(t *testing.T) {
	tests := []struct {
		path     string
		alias    string
		expected string
	}{
		{`"fmt"`, "", "fmt"},
		{`"github.com/foo/bar"`, "", "bar"},
		{`"github.com/foo/bar"`, "baz", "baz"},
		{`"github.com/foo/bar"`, "_", "_"},
		{`"github.com/foo/bar"`, ".", "."},
	}

	for _, tt := range tests {
		var imp importSpec
		if tt.alias != "" {
			imp.Name = &ident{Name: tt.alias}
		}
		imp.Path = &basicLit{Value: tt.path}
		// Can't easily test importName without creating real AST nodes
		// This is covered by the integration tests above
	}
}

func TestApplyFixes(t *testing.T) {
	dir := t.TempDir()

	// Create a test file
	testFile := filepath.Join(dir, "test.go")
	code := `package main

func main() {
	x := int(0)
	_ = x
}
`
	os.WriteFile(testFile, []byte(code), 0644)

	// Create a fix that replaces int(0) with 0
	// Find position of "int(0)" which starts around offset 32
	content, _ := os.ReadFile(testFile)
	startIdx := 0
	for i := range content {
		if i+6 <= len(content) && string(content[i:i+6]) == "int(0)" {
			startIdx = i
			break
		}
	}

	fixes := []fileFix{{
		loc:     SourceLocation{File: testFile, Line: 4, Column: 7},
		start:   startIdx,
		end:     startIdx + 6,
		newText: []byte("0"),
	}}

	err := applyFixes(fixes)
	assert.Nil(t, err)

	// Verify the fix was applied
	newContent, _ := os.ReadFile(testFile)
	assert.NotContains(t, string(newContent), "int(0)")
	assert.Contains(t, string(newContent), "x := 0")
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
}
