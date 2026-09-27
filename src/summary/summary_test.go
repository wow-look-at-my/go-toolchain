package summary

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/bench"
	gotest "github.com/wow-look-at-my/go-toolchain/src/test"
)

func TestGenerateMarkdownEmpty(t *testing.T) {
	t.Serial()
	assert.Empty(t, GenerateMarkdown(nil))
}

func TestGenerateMarkdownBasic(t *testing.T) {
	t.Serial()
	data := &SummaryData{
		Coverage: &gotest.Report{Total: 85.2},
		TestCases: []gotest.TestCaseResult{
			{Package: "example.com/pkg", Test: "TestFoo", Status: "pass", Elapsed: 0.15},
			{Package: "example.com/pkg", Test: "TestBar", Status: "fail", Elapsed: 1.23},
			{Package: "example.com/pkg", Test: "TestSkipped", Status: "skip", Elapsed: 0.0},
		},
	}

	md := GenerateMarkdown(data)

	assert.Contains(t, md, "85.2% coverage")
	assert.Contains(t, md, "**1** passed")
	assert.Contains(t, md, "**1** failed")
	assert.Contains(t, md, "**1** skipped")
	assert.Contains(t, md, "<details>")
	assert.Contains(t, md, "pkg (1 passed, 1 failed, 1 skipped)")
	assert.Contains(t, md, ":white_check_mark:")
	assert.Contains(t, md, ":x:")
	assert.Contains(t, md, ":fast_forward:")
	assert.Contains(t, md, "TestFoo")
	assert.Contains(t, md, "TestBar")
	// Package column should not exist
	assert.NotContains(t, md, "| Package |")
	// Summary row
	assert.Contains(t, md, "**All pkg Tests**")
}

func TestGenerateMarkdownSubtests(t *testing.T) {
	t.Serial()
	data := &SummaryData{
		Coverage: &gotest.Report{Total: 90.0},
		TestCases: []gotest.TestCaseResult{
			{Package: "example.com/pkg", Test: "TestFoo", Status: "pass", Elapsed: 0.10},
			{Package: "example.com/pkg", Test: "TestFoo/case_a", Status: "pass", Elapsed: 0.03},
			{Package: "example.com/pkg", Test: "TestFoo/case_b", Status: "pass", Elapsed: 0.05},
			{Package: "example.com/pkg", Test: "TestFoo/nested/deep", Status: "fail", Elapsed: 0.02},
		},
	}

	md := GenerateMarkdown(data)

	assert.Contains(t, md, "pkg (3 passed, 1 failed)")
	assert.Contains(t, md, "&nbsp;&nbsp;&nbsp;&nbsp;TestFoo/case_a")
	assert.Contains(t, md, "&nbsp;&nbsp;&nbsp;&nbsp;TestFoo/case_b")
	assert.Contains(t, md, "&nbsp;&nbsp;&nbsp;&nbsp;TestFoo/nested/deep")
}

func TestGenerateMarkdownBenchmarks(t *testing.T) {
	t.Serial()
	data := &SummaryData{
		Coverage: &gotest.Report{Total: 80.0},
		Benchmarks: &bench.BenchmarkReport{
			Packages: map[string][]bench.BenchmarkResult{
				"example.com/pkg": {
					{Name: "BenchmarkParse-8", Package: "example.com/pkg", NsPerOp: 1234.5, BytesPerOp: 256, AllocsPerOp: 4},
				},
			},
		},
	}

	md := GenerateMarkdown(data)

	assert.Contains(t, md, "Benchmarks: pkg")
	assert.Contains(t, md, "Parse")
	assert.Contains(t, md, "256 B")
}

func TestGenerateMarkdownBenchComparison(t *testing.T) {
	t.Serial()
	current := &bench.BenchmarkReport{
		Packages: map[string][]bench.BenchmarkResult{
			"example.com/pkg": {
				{Name: "BenchmarkParse-8", Package: "example.com/pkg", NsPerOp: 1000, BytesPerOp: 256, AllocsPerOp: 4},
			},
		},
	}
	prev := &bench.BenchmarkReport{
		Packages: map[string][]bench.BenchmarkResult{
			"example.com/pkg": {
				{Name: "BenchmarkParse-8", Package: "example.com/pkg", NsPerOp: 1200, BytesPerOp: 256, AllocsPerOp: 4},
			},
		},
	}
	comp := bench.Compare(current, prev)
	comp.PreviousCommit = "abc1234"

	data := &SummaryData{
		Coverage:   &gotest.Report{Total: 80.0},
		Benchmarks: current,
		BenchComp:  comp,
	}

	md := GenerateMarkdown(data)

	assert.Contains(t, md, "vs previous")
	assert.Contains(t, md, ":arrow_down:") // faster
	assert.Contains(t, md, "abc1234")
}

func TestGenerateMarkdownSubbenchmarks(t *testing.T) {
	t.Serial()
	data := &SummaryData{
		Coverage: &gotest.Report{Total: 80.0},
		Benchmarks: &bench.BenchmarkReport{
			Packages: map[string][]bench.BenchmarkResult{
				"example.com/pkg": {
					{Name: "BenchmarkParse/small-8", Package: "example.com/pkg", NsPerOp: 500, BytesPerOp: 64, AllocsPerOp: 2},
					{Name: "BenchmarkParse/large-8", Package: "example.com/pkg", NsPerOp: 5000, BytesPerOp: 1024, AllocsPerOp: 10},
				},
			},
		},
	}

	md := GenerateMarkdown(data)

	assert.Contains(t, md, "Parse/small")
	assert.Contains(t, md, "Parse/large")
}

func TestWriteAppendsToFile(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	summaryFile := filepath.Join(tmpDir, "summary.md")

	// Write some existing content
	os.WriteFile(summaryFile, []byte("existing\n"), 0644)

	t.Setenv("GITHUB_STEP_SUMMARY", summaryFile)

	data := &SummaryData{
		Coverage: &gotest.Report{Total: 75.0},
	}

	err := Write(data)
	require.NoError(t, err)

	content, _ := os.ReadFile(summaryFile)
	assert.True(t, strings.HasPrefix(string(content), "existing\n"))
	assert.Contains(t, string(content), "75.0% coverage")
}

func TestWriteNoopWithoutEnv(t *testing.T) {
	t.Serial()
	t.Setenv("GITHUB_STEP_SUMMARY", "")
	assert.NoError(t, Write(&SummaryData{}))
}

func TestPkgToDir(t *testing.T) {
	t.Serial()
	tests := []struct {
		pkg, module, expected string
	}{
		{"github.com/foo/bar/src/cmd", "github.com/foo/bar", "src/cmd"},
		{"github.com/foo/bar", "github.com/foo/bar", "."},
		{"github.com/foo/bar/pkg", "", "pkg"},
		{"standalone", "", ""},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.expected, pkgToDir(tc.pkg, tc.module), "pkgToDir(%q, %q)", tc.pkg, tc.module)
	}
}

func TestRootTestFunc(t *testing.T) {
	t.Serial()
	assert.Equal(t, "TestFoo", rootTestFunc("TestFoo"))
	assert.Equal(t, "TestFoo", rootTestFunc("TestFoo/case_a"))
	assert.Equal(t, "TestFoo", rootTestFunc("TestFoo/nested/deep"))
}

func TestFormatTestName(t *testing.T) {
	t.Serial()
	assert.Equal(t, "TestFoo", formatTestName("TestFoo"))
	assert.Contains(t, formatTestName("TestFoo/bar"), "&nbsp;&nbsp;&nbsp;&nbsp;")
	assert.Contains(t, formatTestName("TestFoo/bar"), "TestFoo/bar")
}

func TestStatusEmoji(t *testing.T) {
	t.Serial()
	assert.Equal(t, ":white_check_mark:", statusEmoji("pass"))
	assert.Equal(t, ":x:", statusEmoji("fail"))
	assert.Equal(t, ":fast_forward:", statusEmoji("skip"))
}

func TestFormatBenchDelta(t *testing.T) {
	t.Serial()
	assert.Contains(t, formatBenchDelta(-5.0), ":arrow_down:")
	assert.Contains(t, formatBenchDelta(5.0), ":arrow_up:")
	assert.Contains(t, formatBenchDelta(0.5), "~0%")
}

func TestBenchDisplayName(t *testing.T) {
	t.Serial()
	assert.Equal(t, "Parse", benchDisplayName("BenchmarkParse-8", "pkg"))
	assert.Equal(t, "Parse/small", benchDisplayName("BenchmarkParse/small-8", "pkg"))
	assert.Equal(t, "Foo", benchDisplayName("BenchmarkFoo-16", "pkg"))
}

func TestFindTestFuncsInDir(t *testing.T) {
	t.Serial()
	// Create a temporary test file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "example_test.go")
	content := `package example

import "testing"

func TestAlpha(t *testing.T) {}
func TestBeta(t *testing.T) {}
func helperNotATest() {}
`
	os.WriteFile(testFile, []byte(content), 0644)

	funcs := findTestFuncsInDir(tmpDir, set.Of("TestAlpha", "TestBeta"))

	assert.Contains(t, funcs, "TestAlpha")
	assert.Contains(t, funcs, "TestBeta")
	assert.Equal(t, 5, funcs["TestAlpha"].line)
	assert.Equal(t, 6, funcs["TestBeta"].line)
}

func TestSourceURL(t *testing.T) {
	t.Serial()
	cache := map[string]testFuncLocation{
		"example.com/pkg.TestFoo": {file: "src/pkg/foo_test.go", line: 42},
	}

	tc := gotest.TestCaseResult{Package: "example.com/pkg", Test: "TestFoo"}
	url := sourceURL(tc, "abc123", "owner/repo", "example.com", cache)
	assert.Contains(t, url, "https://github.com/owner/repo/blob/abc123/src/pkg/foo_test.go#L42")

	// Subtest should link to parent function
	tc2 := gotest.TestCaseResult{Package: "example.com/pkg", Test: "TestFoo/case_a"}
	url2 := sourceURL(tc2, "abc123", "owner/repo", "example.com", cache)
	assert.Contains(t, url2, "foo_test.go#L42")

	// No link when missing SHA
	assert.Empty(t, sourceURL(tc, "", "owner/repo", "example.com", cache))
}

func TestFormatTestNameWithLink(t *testing.T) {
	t.Serial()
	cache := map[string]testFuncLocation{
		"example.com/pkg.TestFoo": {file: "src/pkg/foo_test.go", line: 42},
	}

	// Top-level test with link
	tc := gotest.TestCaseResult{Package: "example.com/pkg", Test: "TestFoo"}
	display := formatTestNameWithLink(tc, "abc123", "owner/repo", "example.com", cache)
	assert.Contains(t, display, "[TestFoo]")
	assert.Contains(t, display, "foo_test.go#L42")
	assert.False(t, strings.HasPrefix(display, "&nbsp;"))

	// Subtest with link and indentation
	tc2 := gotest.TestCaseResult{Package: "example.com/pkg", Test: "TestFoo/case_a"}
	display2 := formatTestNameWithLink(tc2, "abc123", "owner/repo", "example.com", cache)
	assert.True(t, strings.HasPrefix(display2, "&nbsp;&nbsp;&nbsp;&nbsp;"))
	assert.Contains(t, display2, "[TestFoo/case_a]")

	// No link when no SHA
	tc3 := gotest.TestCaseResult{Package: "example.com/pkg", Test: "TestFoo"}
	display3 := formatTestNameWithLink(tc3, "", "owner/repo", "example.com", cache)
	assert.Equal(t, "TestFoo", display3)
}

func TestGenerateMarkdownMultiPackage(t *testing.T) {
	t.Serial()
	data := &SummaryData{
		Coverage: &gotest.Report{Total: 80.0},
		TestCases: []gotest.TestCaseResult{
			{Package: "example.com/cmd", Test: "TestRun", Status: "pass", Elapsed: 0.10},
			{Package: "example.com/cmd", Test: "TestBuild", Status: "pass", Elapsed: 0.20},
			{Package: "example.com/lib", Test: "TestParse", Status: "fail", Elapsed: 0.50},
			{Package: "example.com/lib", Test: "TestFormat", Status: "pass", Elapsed: 0.05},
		},
	}

	md := GenerateMarkdown(data)

	// Each package gets its own collapsed section
	assert.Contains(t, md, "cmd (2 passed)")
	assert.Contains(t, md, "lib (1 passed, 1 failed)")
	// Separate <details> blocks
	assert.Equal(t, 2, strings.Count(md, "<details>"))
	assert.Equal(t, 2, strings.Count(md, "</details>"))
	// Summary rows
	assert.Contains(t, md, "**All cmd Tests**")
	assert.Contains(t, md, "**All lib Tests**")
}

func TestSortTestCases(t *testing.T) {
	t.Serial()
	// Simulate Go's test output order: subtests before parent
	cases := []gotest.TestCaseResult{
		{Test: "TestFoo/case_a", Status: "pass"},
		{Test: "TestFoo/case_b", Status: "pass"},
		{Test: "TestFoo", Status: "pass"},
		{Test: "TestBar/x", Status: "pass"},
		{Test: "TestBar", Status: "pass"},
	}
	sortTestCases(cases)

	// Parent should come before subtests
	assert.Equal(t, "TestFoo", cases[0].Test)
	assert.Equal(t, "TestFoo/case_a", cases[1].Test)
	assert.Equal(t, "TestFoo/case_b", cases[2].Test)
	assert.Equal(t, "TestBar", cases[3].Test)
	assert.Equal(t, "TestBar/x", cases[4].Test)
}

func TestCountTestStatuses(t *testing.T) {
	t.Serial()
	cases := []gotest.TestCaseResult{
		{Status: "pass"}, {Status: "pass"}, {Status: "fail"}, {Status: "skip"},
	}
	p, f, s := countTestStatuses(cases)
	assert.Equal(t, 2, p)
	assert.Equal(t, 1, f)
	assert.Equal(t, 1, s)
}

func TestGenerateMarkdownWithTimeline(t *testing.T) {
	t.Serial()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	data := &SummaryData{
		Coverage: &gotest.Report{Total: 80.0},
		Timeline: []TimelineEntry{
			{Label: "go mod tidy", Thread: "main", Start: base, End: base.Add(500 * time.Millisecond)},
			{Label: "go test", Thread: "main", Start: base.Add(500 * time.Millisecond), End: base.Add(2 * time.Second)},
		},
	}

	md := GenerateMarkdown(data)

	assert.Contains(t, md, "Pipeline Timeline")
	assert.Contains(t, md, "```mermaid")
	assert.Contains(t, md, "gantt")
	assert.Contains(t, md, "go mod tidy")
	assert.Contains(t, md, "go test")
}

func TestGenerateMarkdownWithoutTimeline(t *testing.T) {
	t.Serial()
	data := &SummaryData{
		Coverage: &gotest.Report{Total: 80.0},
	}

	md := GenerateMarkdown(data)

	assert.NotContains(t, md, "Pipeline Timeline")
	assert.NotContains(t, md, "```mermaid")
}
