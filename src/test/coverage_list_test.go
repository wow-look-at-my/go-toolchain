package test

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestPrintItemIndentation(t *testing.T) {
	// Test that columns are aligned and indentation is applied before the name
	item := FileCoverage{
		baseCoverageItem: baseCoverageItem{Statements: 10, Covered: 8},
		File:             "test.go",
	}

	for depth := 0; depth < 3; depth++ {
		output := captureOutput(func() {
			printItem(item, depth)
		})
		if depth == 0 {
			// Depth 0 starts with bold escape code
			assert.True(t, len(output) > 4 && output[:4] == bold, "depth 0: line should start with bold")
		} else {
			// Other depths start with "  " before percentage
			assert.True(t, len(output) > 2 && output[:2] == "  ", "depth %d: line should start with 2 spaces", depth)
		}
		// Name should appear in output
		assert.Contains(t, output, "test.go", "depth %d: should contain filename", depth)
	}
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

func TestPrintEmptyPackage(t *testing.T) {
	// Test that empty packages show ∅ symbol
	item := FileCoverage{
		baseCoverageItem: baseCoverageItem{Statements: 0, Covered: 0},
		File:             "empty.go",
	}

	output := captureOutput(func() {
		printItem(item, 0)
	})
	assert.Contains(t, output, "∅", "empty package should show ∅ symbol")
	assert.Contains(t, output, "empty.go")
}

func TestPrintHidesZeroUncoveredUnlessVerbose(t *testing.T) {
	report := Report{
		Packages: []PackageCoverage{
			{
				baseCoverageItem: baseCoverageItem{Statements: 10, Covered: 5},
				Package:          "pkg",
				Files: []FileCoverage{
					{baseCoverageItem: baseCoverageItem{Statements: 5, Covered: 5}, File: "full.go"},
					{baseCoverageItem: baseCoverageItem{Statements: 5, Covered: 0}, File: "partial.go"},
				},
			},
		},
	}

	// Without verbose, should hide 0-uncovered files
	output := captureOutput(func() {
		report.Print(PrintOptions{ShowFiles: true, Verbose: false})
	})
	assert.NotContains(t, output, "full.go", "should hide fully covered file")
	assert.Contains(t, output, "partial.go", "should show file with uncovered code")

	// With verbose, should show all files
	outputVerbose := captureOutput(func() {
		report.Print(PrintOptions{ShowFiles: true, Verbose: true})
	})
	assert.Contains(t, outputVerbose, "full.go", "verbose should show fully covered file")
	assert.Contains(t, outputVerbose, "partial.go", "verbose should show file with uncovered code")
}
