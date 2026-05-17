package test

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func captureOutput(f func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	f()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String()
}

func TestParseCoverageStatements(t *testing.T) {
	// Create temp file with coverage data
	content := `mode: set
example.com/pkg/foo.go:10.1,12.1 3 1
example.com/pkg/foo.go:14.1,16.1 5 0
example.com/pkg/bar.go:10.1,12.1 10 1
example.com/pkg/bar.go:14.1,16.1 4 0
`
	f, err := os.CreateTemp("", "coverage*.out")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	_, err = f.WriteString(content)
	require.NoError(t, err)
	f.Close()

	total, files, err := ParseProfile(f.Name())
	require.NoError(t, err)

	// Verify total coverage: 13 covered / 22 total = 59.09%
	assert.InDelta(t, 59.09, float64(total), 0.1)

	// Verify file-level statement counts
	fileMap := make(map[string]FileCoverage)
	for _, fc := range files {
		fileMap[fc.File] = fc
	}

	// foo.go: 3 covered, 8 total (5 uncovered)
	foo := fileMap["example.com/pkg/foo.go"]
	assert.Equal(t, 8, foo.Statements)
	assert.Equal(t, 3, foo.Covered)

	// bar.go: 10 covered, 14 total (4 uncovered)
	bar := fileMap["example.com/pkg/bar.go"]
	assert.Equal(t, 14, bar.Statements)
	assert.Equal(t, 10, bar.Covered)
}

func TestSortByUncovered(t *testing.T) {
	files := []FileCoverage{
		{baseCoverageItem: baseCoverageItem{Statements: 8, Covered: 3}, File: "example.com/pkg/foo.go"},  // 5 uncovered
		{baseCoverageItem: baseCoverageItem{Statements: 14, Covered: 10}, File: "example.com/pkg/bar.go"}, // 4 uncovered
	}

	sortByUncovered(files)

	// foo.go has 5 uncovered, bar.go has 4 uncovered
	// So foo should come first
	assert.Equal(t, "example.com/pkg/foo.go", files[0].File)
}

func TestPrintTargetGroupNoOSC8InCI(t *testing.T) {
	t.Setenv("CI", "true")

	file := FileCoverage{
		baseCoverageItem: baseCoverageItem{Statements: 10, Covered: 0},
		File:             "example.com/pkg/test.go",
	}
	fn := FuncCoverage{
		baseCoverageItem: baseCoverageItem{Statements: 10, Covered: 0},
		FuncLine:         5,
		Function:         "MyFunc",
		File:             &file,
	}
	file.Functions = []FuncCoverage{fn}

	report := Report{
		Packages: []PackageCoverage{
			{
				baseCoverageItem: baseCoverageItem{Statements: 10, Covered: 0},
				Package:          "example.com/pkg",
				Files:            []FileCoverage{file},
			},
		},
	}

	output := captureOutput(func() {
		report.Print()
	})

	assert.NotContains(t, output, "\033]8;;", "should not contain OSC 8 escape sequences in CI")
	assert.Contains(t, output, "MyFunc")
	assert.Contains(t, output, "test.go:5")
}

func TestDimText(t *testing.T) {
	// Full brightness
	assert.Equal(t, "\033[38;2;255;255;255m", dimText(1.0))
	// Half brightness
	assert.Equal(t, "\033[38;2;127;127;127m", dimText(0.5))
	// Zero brightness
	assert.Equal(t, "\033[38;2;0;0;0m", dimText(0.0))
}

func TestHsvToRGB(t *testing.T) {
	// Red (0°)
	r, g, b := hsvToRGB(0, 1.0, 1.0)
	assert.Equal(t, uint8(255), r)
	assert.Equal(t, uint8(0), g)
	assert.Equal(t, uint8(0), b)

	// Green (120°)
	r, g, b = hsvToRGB(120, 1.0, 1.0)
	assert.Equal(t, uint8(0), r)
	assert.Equal(t, uint8(255), g)
	assert.Equal(t, uint8(0), b)

	// Blue (240°)
	r, g, b = hsvToRGB(240, 1.0, 1.0)
	assert.Equal(t, uint8(0), r)
	assert.Equal(t, uint8(0), g)
	assert.Equal(t, uint8(255), b)

	// Cyan (180°)
	r, g, b = hsvToRGB(180, 1.0, 1.0)
	assert.Equal(t, uint8(0), r)
	assert.Equal(t, uint8(255), g)
	assert.Equal(t, uint8(255), b)

	// Magenta (300°)
	r, g, b = hsvToRGB(300, 1.0, 1.0)
	assert.Equal(t, uint8(255), r)
	assert.Equal(t, uint8(0), g)
	assert.Equal(t, uint8(255), b)

	// Yellow (60°)
	r, g, b = hsvToRGB(60, 1.0, 1.0)
	assert.Equal(t, uint8(255), r)
	assert.Equal(t, uint8(255), g)
	assert.Equal(t, uint8(0), b)
}

func TestColorGain(t *testing.T) {
	// High gain should produce green-ish color
	high := colorGain(1.0)
	assert.Contains(t, high, "+ 1.0%")

	// Low gain should produce red-ish color
	low := colorGain(0.1)
	assert.Contains(t, low, "+ 0.1%")

	// Very high gain gets capped at green (hue=120)
	capped := colorGain(5.0)
	assert.Contains(t, capped, "+ 5.0%")
}

func TestShortFile(t *testing.T) {
	assert.Equal(t, "pkg/file.go", shortFile("example.com/org/pkg/file.go"))
	assert.Equal(t, "src/main.go", shortFile("github.com/user/repo/src/main.go"))
	assert.Equal(t, "file.go", shortFile("file.go"))
	assert.Equal(t, "", shortFile(""))
}

func TestPrintCapsAtFivePerGroup(t *testing.T) {
	t.Setenv("CI", "true")

	// Create 7 untested functions and 7 partial functions — only 5 of each should appear
	var files []FileCoverage
	for i := range 7 {
		f := FileCoverage{
			baseCoverageItem: baseCoverageItem{Statements: 10 + i, Covered: 0},
			File:             fmt.Sprintf("example.com/pkg/untested%d.go", i),
		}
		fn := FuncCoverage{
			baseCoverageItem: baseCoverageItem{Statements: 10 + i, Covered: 0},
			FuncLine:         1,
			Function:         fmt.Sprintf("Untested%d", i),
			File:             &f,
		}
		f.Functions = []FuncCoverage{fn}
		files = append(files, f)
	}
	for i := range 7 {
		f := FileCoverage{
			baseCoverageItem: baseCoverageItem{Statements: 20 + i, Covered: 1},
			File:             fmt.Sprintf("example.com/pkg/partial%d.go", i),
		}
		fn := FuncCoverage{
			baseCoverageItem: baseCoverageItem{Statements: 20 + i, Covered: 1},
			FuncLine:         1,
			Function:         fmt.Sprintf("Partial%d", i),
			File:             &f,
		}
		f.Functions = []FuncCoverage{fn}
		files = append(files, f)
	}

	report := Report{
		Packages: []PackageCoverage{
			{
				baseCoverageItem: baseCoverageItem{Statements: 300, Covered: 7},
				Package:          "example.com/pkg",
				Files:            files,
			},
		},
	}

	output := captureOutput(func() {
		report.Print()
	})

	// Untested: 5 largest should appear (Untested6=16, ..., Untested2=12)
	assert.Contains(t, output, "Untested6")
	assert.Contains(t, output, "Untested2")
	assert.NotContains(t, output, "Untested0")
	assert.NotContains(t, output, "Untested1")

	// Partial: 5 largest should appear (Partial6=25 uncov, ..., Partial2=21 uncov)
	assert.Contains(t, output, "Partial6")
	assert.Contains(t, output, "Partial2")
	assert.NotContains(t, output, "Partial0")
	assert.NotContains(t, output, "Partial1")
}

func TestPrintEmptyReport(t *testing.T) {
	report := Report{
		Packages: []PackageCoverage{
			{
				baseCoverageItem: baseCoverageItem{Statements: 10, Covered: 10},
				Package:          "example.com/pkg",
			},
		},
	}

	output := captureOutput(func() {
		report.Print()
	})

	// Fully covered: no targets to show
	assert.NotContains(t, output, "UNTESTED")
	assert.NotContains(t, output, "PARTIAL")
}

func TestPrintShowsTopUncoveredFunctions(t *testing.T) {
	t.Setenv("CI", "true")

	// File with a partially-covered function (has Covered > 0)
	file1 := FileCoverage{
		baseCoverageItem: baseCoverageItem{Statements: 20, Covered: 10},
		File:             "example.com/pkg/partial.go",
	}
	fn1 := FuncCoverage{
		baseCoverageItem: baseCoverageItem{Statements: 10, Covered: 5},
		FuncLine:         10,
		Function:         "NeedsCoverage",
		File:             &file1,
	}
	file1.Functions = []FuncCoverage{fn1}

	// File with a fully-covered function (no uncovered lines)
	file2 := FileCoverage{
		baseCoverageItem: baseCoverageItem{Statements: 5, Covered: 5},
		File:             "example.com/pkg/full.go",
	}
	fn2 := FuncCoverage{
		baseCoverageItem: baseCoverageItem{Statements: 5, Covered: 5},
		FuncLine:         20,
		Function:         "FullyCovered",
		File:             &file2,
	}
	file2.Functions = []FuncCoverage{fn2}

	// File with a completely untested function (Covered == 0)
	file3 := FileCoverage{
		baseCoverageItem: baseCoverageItem{Statements: 30, Covered: 0},
		File:             "example.com/pkg/untested.go",
	}
	fn3 := FuncCoverage{
		baseCoverageItem: baseCoverageItem{Statements: 30, Covered: 0},
		FuncLine:         5,
		Function:         "NeverCalled",
		File:             &file3,
	}
	file3.Functions = []FuncCoverage{fn3}

	report := Report{
		Packages: []PackageCoverage{
			{
				baseCoverageItem: baseCoverageItem{Statements: 55, Covered: 15},
				Package:          "example.com/pkg",
				Files:            []FileCoverage{file1, file2, file3},
			},
		},
	}

	output := captureOutput(func() {
		report.Print()
	})

	// Should show UNTESTED section header (for 0%-covered functions)
	assert.Contains(t, output, "UNTESTED", "should show UNTESTED section")

	// Should show PARTIAL section header (for partially-covered functions)
	assert.Contains(t, output, "PARTIAL", "should show PARTIAL section")

	// Should show the untested function with its gain and location
	assert.Contains(t, output, "NeverCalled", "should show untested function")
	assert.Contains(t, output, "untested.go:5", "should show file:line for untested function")
	assert.Contains(t, output, "30 stmts", "should show uncovered statement count")

	// Should show the partially-covered function
	assert.Contains(t, output, "NeedsCoverage", "should show partially covered function")
	assert.Contains(t, output, "partial.go:10", "should show file:line for partial function")

	// Should NOT show fully covered function
	assert.NotContains(t, output, "FullyCovered", "should not show covered function")
	assert.NotContains(t, output, "full.go", "should not show fully covered file")

	// Untested function (30 lines) should appear before partial (5 lines)
	// because it has more uncovered lines and thus higher gain
	untestedIdx := strings.Index(output, "NeverCalled")
	partialIdx := strings.Index(output, "NeedsCoverage")
	assert.True(t, untestedIdx < partialIdx,
		"untested function should appear before partial (untested=%d, partial=%d)", untestedIdx, partialIdx)
}
