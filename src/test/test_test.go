package test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"gotest.tools/gotestsum/testjson"
)

func TestCoverageHandlerExtractsCoverage(t *testing.T) {
	h := &coverageHandler{coverage: make(map[string]float32)}

	// Simulate output event with coverage info
	event := testjson.TestEvent{
		Action:  testjson.ActionOutput,
		Package: "example.com/pkg",
		Output:  "coverage: 75.5% of statements\n",
	}

	require.NoError(t, h.Event(event, nil))

	assert.Equal(t, float32(75.5), h.coverage["example.com/pkg"])
}

func TestCoverageHandlerIgnoresNonCoverageOutput(t *testing.T) {
	h := &coverageHandler{coverage: make(map[string]float32)}

	event := testjson.TestEvent{
		Action:  testjson.ActionOutput,
		Package: "example.com/pkg",
		Output:  "=== RUN TestFoo\n",
	}

	require.NoError(t, h.Event(event, nil))

	_, exists := h.coverage["example.com/pkg"]
	assert.False(t, exists)
}

func TestCoverageHandlerIgnoresNonOutputActions(t *testing.T) {
	h := &coverageHandler{coverage: make(map[string]float32)}

	event := testjson.TestEvent{
		Action:  testjson.ActionPass,
		Package: "example.com/pkg",
	}

	require.NoError(t, h.Event(event, nil))

	_, exists := h.coverage["example.com/pkg"]
	assert.False(t, exists)
}

func TestCoverageHandlerErr(t *testing.T) {
	h := &coverageHandler{coverage: make(map[string]float32)}
	assert.NoError(t, h.Err("some error"))
}

func TestCoverageRegex(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"coverage: 80.0% of statements", "80.0"},
		{"coverage: 100% of statements", "100"},
		{"coverage: 0.0% of statements", "0.0"},
		{"coverage: 45.5% of statements", "45.5"},
		{"no coverage here", ""},
	}

	for _, tc := range tests {
		matches := coverageRe.FindStringSubmatch(tc.input)
		if tc.expected == "" {
			assert.LessOrEqual(t, len(matches), 0)
		} else {
			require.Equal(t, 2, len(matches), "input %q: expected match, got %v", tc.input, matches)
			assert.Equal(t, tc.expected, matches[1], "input %q", tc.input)
		}
	}
}

func TestCoverageHandlerMultiplePackages(t *testing.T) {
	h := &coverageHandler{coverage: make(map[string]float32)}

	events := []testjson.TestEvent{
		{Action: testjson.ActionOutput, Package: "pkg1", Output: "coverage: 50.0% of statements\n"},
		{Action: testjson.ActionOutput, Package: "pkg2", Output: "coverage: 75.0% of statements\n"},
		{Action: testjson.ActionOutput, Package: "pkg3", Output: "coverage: 100% of statements\n"},
	}

	for _, event := range events {
		require.NoError(t, h.Event(event, nil))
	}

	assert.Equal(t, float32(50.0), h.coverage["pkg1"])
	assert.Equal(t, float32(75.0), h.coverage["pkg2"])
	assert.Equal(t, float32(100.0), h.coverage["pkg3"])
}

func TestRunTestsWithMock(t *testing.T) {
	coverFile := filepath.Join(t.TempDir(), "coverage.out")

	// Create coverage file for ParseProfile - 17 covered, 3 uncovered = 85%
	coverContent := `mode: set
example.com/pkg/main.go:10.20,12.2 17 1
example.com/pkg/main.go:14.20,16.2 3 0
`
	os.WriteFile(coverFile, []byte(coverContent), 0644)

	mock := runner.NewMock()
	// Return valid JSON test output
	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"coverage: 85.0% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg"}
`
	mock.SetResponse("go", []string{"test", "-vet=off", "-json", "-timeout=30s", "-coverprofile=" + coverFile, "./..."}, []byte(testOutput), nil)

	result, err := RunTests(mock, false, coverFile, 0, nil)
	require.Nil(t, err)

	assert.Equal(t, 1, len(result.Coverage.Packages))

	assert.Equal(t, float32(85.0), result.Coverage.Packages[0].Pct())
}

func TestRunTestsFailure(t *testing.T) {
	coverFile := filepath.Join(t.TempDir(), "coverage.out")

	mock := runner.NewMock()
	mock.SetResponse("go", []string{"test", "-vet=off", "-json", "-timeout=30s", "-coverprofile=" + coverFile, "./..."}, nil, fmt.Errorf("test failed"))

	_, err := RunTests(mock, false, coverFile, 0, nil)
	assert.NotNil(t, err)
}

func TestRunTestsVerbose(t *testing.T) {
	coverFile := filepath.Join(t.TempDir(), "coverage.out")

	// Create coverage file for ParseProfile
	coverContent := `mode: set
example.com/pkg/main.go:10.20,12.2 1 1
`
	os.WriteFile(coverFile, []byte(coverContent), 0644)

	mock := runner.NewMock()
	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"=== RUN TestFoo\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"output","Package":"example.com/pkg","Output":"coverage: 85.0% of statements\n"}
{"Time":"2024-01-01T00:00:03Z","Action":"pass","Package":"example.com/pkg"}
`
	mock.SetResponse("go", []string{"test", "-vet=off", "-json", "-timeout=30s", "-coverprofile=" + coverFile, "./..."}, []byte(testOutput), nil)

	result, err := RunTests(mock, true, coverFile, 0, nil) // verbose=true
	require.Nil(t, err)

	assert.Equal(t, 1, len(result.Coverage.Packages))
}

func TestRunTestsNoCoverageFile(t *testing.T) {
	coverFile := filepath.Join(t.TempDir(), "coverage.out")
	// Don't create coverage.out - no profile means no statement-level data

	mock := runner.NewMock()
	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"pkg1"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"pkg1","Output":"coverage: 50.0% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"pkg1"}
{"Time":"2024-01-01T00:00:03Z","Action":"run","Package":"pkg2"}
{"Time":"2024-01-01T00:00:04Z","Action":"output","Package":"pkg2","Output":"coverage: 100% of statements\n"}
{"Time":"2024-01-01T00:00:05Z","Action":"pass","Package":"pkg2"}
`
	mock.SetResponse("go", []string{"test", "-vet=off", "-json", "-timeout=30s", "-coverprofile=" + coverFile, "./..."}, []byte(testOutput), nil)

	result, err := RunTests(mock, false, coverFile, 0, nil)
	require.Nil(t, err)

	// Without a coverage profile we can't compute statement-weighted total.
	// Total should be 0 rather than a misleading per-package average.
	assert.Equal(t, float32(0), result.Coverage.Total)

	// Per-package percentages are still available from test output
	assert.Equal(t, 2, len(result.Coverage.Packages))
}

func TestRunTestsNoStatementsMarkedCorrectly(t *testing.T) {
	coverFile := filepath.Join(t.TempDir(), "coverage.out")

	// Profile only has pkg1 and pkg2 data; pkg3 has no statements
	// pkg1: 1 covered + 1 uncovered = 50%, pkg2: 2 covered = 100%
	// total: 3 covered / 4 statements = 75%
	coverContent := `mode: set
example.com/pkg1/main.go:10.20,12.2 1 1
example.com/pkg1/main.go:14.20,16.2 1 0
example.com/pkg2/main.go:10.20,12.2 2 1
`
	os.WriteFile(coverFile, []byte(coverContent), 0644)

	mock := runner.NewMock()
	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg1"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg1","Output":"coverage: 50.0% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg1"}
{"Time":"2024-01-01T00:00:03Z","Action":"run","Package":"example.com/pkg2"}
{"Time":"2024-01-01T00:00:04Z","Action":"output","Package":"example.com/pkg2","Output":"coverage: 100% of statements\n"}
{"Time":"2024-01-01T00:00:05Z","Action":"pass","Package":"example.com/pkg2"}
{"Time":"2024-01-01T00:00:06Z","Action":"run","Package":"example.com/pkg3"}
{"Time":"2024-01-01T00:00:07Z","Action":"output","Package":"example.com/pkg3","Output":"coverage: [no statements]\n"}
{"Time":"2024-01-01T00:00:08Z","Action":"pass","Package":"example.com/pkg3"}
`
	mock.SetResponse("go", []string{"test", "-vet=off", "-json", "-timeout=30s", "-coverprofile=" + coverFile, "./..."}, []byte(testOutput), nil)

	result, err := RunTests(mock, false, coverFile, 0, nil)
	require.Nil(t, err)

	// Total from profile: 3 covered / 4 statements = 75%
	// (statement-weighted, not per-package average)
	assert.Equal(t, float32(75.0), result.Coverage.Total)

	// Verify statements are set correctly
	for _, p := range result.Coverage.Packages {
		switch p.Package {
		case "example.com/pkg1", "example.com/pkg2":
			assert.NotEqual(t, 0, p.Statements)
		case "example.com/pkg3":
			assert.Equal(t, 0, p.Statements)
		}
	}
}

func TestRunTestsNoStatementsWithProfile(t *testing.T) {
	coverFile := filepath.Join(t.TempDir(), "coverage.out")

	// Create coverage file that only has data for pkg1 (pkg2 has no statements)
	coverContent := `mode: set
example.com/pkg1/main.go:10.20,12.2 1 1
example.com/pkg1/main.go:14.20,16.2 1 0
`
	os.WriteFile(coverFile, []byte(coverContent), 0644)

	mock := runner.NewMock()
	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg1"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg1","Output":"coverage: 50.0% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg1"}
{"Time":"2024-01-01T00:00:03Z","Action":"run","Package":"example.com/pkg2"}
{"Time":"2024-01-01T00:00:04Z","Action":"output","Package":"example.com/pkg2","Output":"coverage: [no statements]\n"}
{"Time":"2024-01-01T00:00:05Z","Action":"pass","Package":"example.com/pkg2"}
`
	mock.SetResponse("go", []string{"test", "-vet=off", "-json", "-timeout=30s", "-coverprofile=" + coverFile, "./..."}, []byte(testOutput), nil)

	result, err := RunTests(mock, false, coverFile, 0, nil)
	require.Nil(t, err)

	// Total comes from ParseProfile: 1 covered / 2 statements = 50%
	assert.Equal(t, float32(50.0), result.Coverage.Total)

	// Verify statements are set on the right package
	for _, p := range result.Coverage.Packages {
		assert.False(t, p.Package == "example.com/pkg2" && p.Statements != 0)
		assert.False(t, p.Package == "example.com/pkg1" && p.Statements == 0)
	}
}

func TestShortPkg(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"github.com/wow-look-at-my/go-toolchain/src/cmd", "cmd"},
		{"github.com/foo/bar", "bar"},
		{"standalone", "standalone"},
		{"a/b", "b"},
		{"", ""},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.expected, shortPkg(tc.input), "shortPkg(%q)", tc.input)
	}
}

func TestRealtimePassOutput(t *testing.T) {
	var buf bytes.Buffer
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: make(map[string]bool),
	}

	event := testjson.TestEvent{
		Action:  testjson.ActionPass,
		Package: "github.com/example/pkg",
		Test:    "TestFoo",
		Elapsed: 0.15,
	}
	require.NoError(t, h.Event(event, nil))

	output := buf.String()
	assert.Contains(t, output, "done.")
	assert.Contains(t, output, "pkg.TestFoo...")
	assert.Contains(t, output, "0.15s")
}

func TestRealtimePassOutputHiddenWhenFast(t *testing.T) {
	var buf bytes.Buffer
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: make(map[string]bool),
	}

	event := testjson.TestEvent{
		Action:  testjson.ActionPass,
		Package: "github.com/example/pkg",
		Test:    "TestFoo",
		Elapsed: 0.05,
	}
	require.NoError(t, h.Event(event, nil))

	assert.Empty(t, buf.String(), "passing tests under 0.1s should be hidden")
}

func TestRealtimeFailOutput(t *testing.T) {
	var buf bytes.Buffer
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: make(map[string]bool),
	}

	event := testjson.TestEvent{
		Action:  testjson.ActionFail,
		Package: "github.com/example/pkg",
		Test:    "TestBar",
		Elapsed: 1.23,
	}
	require.NoError(t, h.Event(event, nil))

	output := buf.String()
	assert.Contains(t, output, "failed!")
	assert.Contains(t, output, "pkg.TestBar...")
	assert.Contains(t, output, "1.23s")
}

func TestRealtimeTimeoutOutput(t *testing.T) {
	var buf bytes.Buffer
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: make(map[string]bool),
		timedOut:   make(map[string]bool),
	}

	// First, simulate timeout output event
	outputEvent := testjson.TestEvent{
		Action:  testjson.ActionOutput,
		Package: "github.com/example/pkg",
		Test:    "TestSlow",
		Output:  "panic: test timed out after 30s\n",
	}
	require.NoError(t, h.Event(outputEvent, nil))

	// Then simulate the fail event
	failEvent := testjson.TestEvent{
		Action:  testjson.ActionFail,
		Package: "github.com/example/pkg",
		Test:    "TestSlow",
		Elapsed: 30.0,
	}
	require.NoError(t, h.Event(failEvent, nil))

	output := buf.String()
	assert.Contains(t, output, "timed out!")
	assert.Contains(t, output, "pkg.TestSlow...")
	assert.NotContains(t, output, "failed!")
}

func TestRealtimeSkipOutput(t *testing.T) {
	var buf bytes.Buffer
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: make(map[string]bool),
	}

	event := testjson.TestEvent{
		Action:  testjson.ActionSkip,
		Package: "github.com/example/pkg",
		Test:    "TestSkipped",
		Elapsed: 0.5,
	}
	require.NoError(t, h.Event(event, nil))

	output := buf.String()
	assert.Contains(t, output, "skipped.")
	assert.Contains(t, output, "pkg.TestSkipped...")
}

func TestRealtimeSkipOutputHiddenWhenFast(t *testing.T) {
	var buf bytes.Buffer
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: make(map[string]bool),
	}

	event := testjson.TestEvent{
		Action:  testjson.ActionSkip,
		Package: "github.com/example/pkg",
		Test:    "TestSkipped",
		Elapsed: 0.0,
	}
	require.NoError(t, h.Event(event, nil))

	assert.Empty(t, buf.String(), "skipped tests under 0.1s should be hidden")
}

func TestRealtimeNoOutputInVerboseMode(t *testing.T) {
	var buf bytes.Buffer
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		verbose:    true,
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: make(map[string]bool),
	}

	event := testjson.TestEvent{
		Action:  testjson.ActionPass,
		Package: "github.com/example/pkg",
		Test:    "TestFoo",
		Elapsed: 0.05,
	}
	require.NoError(t, h.Event(event, nil))

	assert.Empty(t, buf.String(), "verbose mode should not print status lines")
}

func TestRealtimeNoOutputForPackageEvents(t *testing.T) {
	var buf bytes.Buffer
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: make(map[string]bool),
	}

	// Package-level pass (Test is empty)
	event := testjson.TestEvent{
		Action:  testjson.ActionPass,
		Package: "github.com/example/pkg",
		Elapsed: 2.5,
	}
	require.NoError(t, h.Event(event, nil))

	assert.Empty(t, buf.String(), "package-level events should not print status lines")
}

func TestRunTestsPackagesContainFiles(t *testing.T) {
	coverFile := filepath.Join(t.TempDir(), "coverage.out")

	// Coverage profile with two files in pkg1 and one in pkg2
	coverContent := `mode: set
example.com/pkg1/foo.go:10.20,12.2 2 1
example.com/pkg1/bar.go:10.20,12.2 3 1
example.com/pkg2/baz.go:10.20,12.2 5 0
`
	os.WriteFile(coverFile, []byte(coverContent), 0644)

	mock := runner.NewMock()
	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg1"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg1","Output":"coverage: 100% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg1"}
{"Time":"2024-01-01T00:00:03Z","Action":"run","Package":"example.com/pkg2"}
{"Time":"2024-01-01T00:00:04Z","Action":"output","Package":"example.com/pkg2","Output":"coverage: 0% of statements\n"}
{"Time":"2024-01-01T00:00:05Z","Action":"pass","Package":"example.com/pkg2"}
`
	mock.SetResponse("go", []string{"test", "-vet=off", "-json", "-timeout=30s", "-coverprofile=" + coverFile, "./..."}, []byte(testOutput), nil)

	result, err := RunTests(mock, false, coverFile, 0, nil)
	require.Nil(t, err)

	// Verify packages contain their files
	for _, p := range result.Coverage.Packages {
		switch p.Package {
		case "example.com/pkg1":
			assert.Equal(t, 2, len(p.Files))
		case "example.com/pkg2":
			assert.Equal(t, 1, len(p.Files))
		}
	}
}

func TestRunTestsGo125ListsTestPackages(t *testing.T) {
	coverFile := filepath.Join(t.TempDir(), "coverage.out")

	coverContent := `mode: set
example.com/pkg1/main.go:10.20,12.2 17 1
example.com/pkg1/main.go:14.20,16.2 3 0
`
	os.WriteFile(coverFile, []byte(coverContent), 0644)

	mock := runner.NewMock()

	// Mock go list to return one test package
	goListOutput := "example.com/pkg1\n"
	mock.SetResponse("go", []string{"list", "-f", `{{if .TestGoFiles}}{{.ImportPath}}{{end}}`, "./..."}, []byte(goListOutput), nil)

	// Mock go test with the specific packages (not ./...)
	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg1"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg1","Output":"coverage: 85.0% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg1"}
`
	mock.SetResponse("go", []string{"test", "-vet=off", "-json", "-timeout=30s", "-coverprofile=" + coverFile, "-coverpkg=./...", "example.com/pkg1"}, []byte(testOutput), nil)

	result, err := RunTests(mock, false, coverFile, 25, nil)
	require.Nil(t, err)

	assert.Equal(t, 1, len(result.Coverage.Packages))
	assert.Equal(t, float32(85.0), result.Coverage.Packages[0].Pct())

	// Verify go list was called
	calls := mock.Calls()
	var gotList bool
	for _, c := range calls {
		if c.IsCmd("go", "list") {
			gotList = true
		}
	}
	assert.True(t, gotList, "expected go list call for Go 1.25")
}

func TestRunTestsGo125FallsBackOnListFailure(t *testing.T) {
	coverFile := filepath.Join(t.TempDir(), "coverage.out")

	coverContent := `mode: set
example.com/pkg/main.go:10.20,12.2 1 1
`
	os.WriteFile(coverFile, []byte(coverContent), 0644)

	mock := runner.NewMock()

	// Mock go list to fail
	mock.SetResponse("go", []string{"list", "-f", `{{if .TestGoFiles}}{{.ImportPath}}{{end}}`, "./..."}, nil, fmt.Errorf("go list failed"))

	// Mock go test with ./... (fallback)
	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"coverage: 100% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg"}
`
	mock.SetResponse("go", []string{"test", "-vet=off", "-json", "-timeout=30s", "-coverprofile=" + coverFile, "./..."}, []byte(testOutput), nil)

	result, err := RunTests(mock, false, coverFile, 25, nil)
	require.Nil(t, err)
	assert.Equal(t, 1, len(result.Coverage.Packages))
}

func TestRunTestsGo124UsesEllipsis(t *testing.T) {
	coverFile := filepath.Join(t.TempDir(), "coverage.out")

	coverContent := `mode: set
example.com/pkg/main.go:10.20,12.2 1 1
`
	os.WriteFile(coverFile, []byte(coverContent), 0644)

	mock := runner.NewMock()

	// Mock go test with ./... (pre-1.25 behavior)
	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"coverage: 100% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg"}
`
	mock.SetResponse("go", []string{"test", "-vet=off", "-json", "-timeout=30s", "-coverprofile=" + coverFile, "./..."}, []byte(testOutput), nil)

	result, err := RunTests(mock, false, coverFile, 24, nil)
	require.Nil(t, err)
	assert.Equal(t, 1, len(result.Coverage.Packages))

	// Verify no go list call with TestGoFiles template was made (ReachablePackages
	// also calls go list, but with different args)
	calls := mock.Calls()
	for _, c := range calls {
		if c.IsCmd("go", "list") && c.HasArg("{{if .TestGoFiles}}{{.ImportPath}}{{end}}") {
			t.Fatal("listTestPackages should not be called for Go 1.24")
		}
	}
}
