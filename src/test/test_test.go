package test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

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

	if err := h.Event(event, nil); err != nil {
		t.Fatalf("Event returned error: %v", err)
	}

	if h.coverage["example.com/pkg"] != 75.5 {
		t.Errorf("expected coverage 75.5, got %v", h.coverage["example.com/pkg"])
	}
}

func TestCoverageHandlerIgnoresNonCoverageOutput(t *testing.T) {
	h := &coverageHandler{coverage: make(map[string]float32)}

	event := testjson.TestEvent{
		Action:  testjson.ActionOutput,
		Package: "example.com/pkg",
		Output:  "=== RUN TestFoo\n",
	}

	if err := h.Event(event, nil); err != nil {
		t.Fatalf("Event returned error: %v", err)
	}

	if _, exists := h.coverage["example.com/pkg"]; exists {
		t.Error("should not have extracted coverage from non-coverage output")
	}
}

func TestCoverageHandlerIgnoresNonOutputActions(t *testing.T) {
	h := &coverageHandler{coverage: make(map[string]float32)}

	event := testjson.TestEvent{
		Action:  testjson.ActionPass,
		Package: "example.com/pkg",
	}

	if err := h.Event(event, nil); err != nil {
		t.Fatalf("Event returned error: %v", err)
	}

	if _, exists := h.coverage["example.com/pkg"]; exists {
		t.Error("should not have extracted coverage from non-output action")
	}
}

func TestCoverageHandlerErr(t *testing.T) {
	h := &coverageHandler{coverage: make(map[string]float32)}
	if err := h.Err("some error"); err != nil {
		t.Errorf("Err should return nil, got %v", err)
	}
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
			if len(matches) > 0 {
				t.Errorf("input %q: expected no match, got %v", tc.input, matches)
			}
		} else {
			if len(matches) != 2 {
				t.Errorf("input %q: expected match, got %v", tc.input, matches)
			} else if matches[1] != tc.expected {
				t.Errorf("input %q: expected %q, got %q", tc.input, tc.expected, matches[1])
			}
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
		if err := h.Event(event, nil); err != nil {
			t.Fatalf("Event returned error: %v", err)
		}
	}

	if h.coverage["pkg1"] != 50.0 {
		t.Errorf("pkg1: expected 50.0, got %v", h.coverage["pkg1"])
	}
	if h.coverage["pkg2"] != 75.0 {
		t.Errorf("pkg2: expected 75.0, got %v", h.coverage["pkg2"])
	}
	if h.coverage["pkg3"] != 100.0 {
		t.Errorf("pkg3: expected 100.0, got %v", h.coverage["pkg3"])
	}
}

func TestRunTestsWithMock(t *testing.T) {
	coverFile := filepath.Join(t.TempDir(), "coverage.out")

	// Create coverage file for ParseProfile - 17 covered, 3 uncovered = 85%
	coverContent := `mode: set
example.com/pkg/main.go:10.20,12.2 17 1
example.com/pkg/main.go:14.20,16.2 3 0
`
	os.WriteFile(coverFile, []byte(coverContent), 0644)

	mock := NewMockRunner()
	// Return valid JSON test output
	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"coverage: 85.0% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg"}
`
	mock.SetResponse("go", []string{"test", "-vet=off", "-json", "-coverprofile=" + coverFile, "./..."}, []byte(testOutput), nil)

	result, err := RunTests(mock, false, coverFile)
	if err != nil {
		t.Fatalf("runTests failed: %v", err)
	}

	if len(result.Coverage.Packages) != 1 {
		t.Errorf("expected 1 package, got %d", len(result.Coverage.Packages))
	}

	if result.Coverage.Packages[0].Pct() != 85.0 {
		t.Errorf("expected coverage 85.0, got %v", result.Coverage.Packages[0].Pct())
	}
}

func TestRunTestsFailure(t *testing.T) {
	coverFile := filepath.Join(t.TempDir(), "coverage.out")

	mock := NewMockRunner()
	mock.SetResponse("go", []string{"test", "-vet=off", "-json", "-coverprofile=" + coverFile, "./..."}, nil, fmt.Errorf("test failed"))

	_, err := RunTests(mock, false, coverFile)
	if err == nil {
		t.Error("expected error when tests fail")
	}
}

func TestRunTestsVerbose(t *testing.T) {
	coverFile := filepath.Join(t.TempDir(), "coverage.out")

	// Create coverage file for ParseProfile
	coverContent := `mode: set
example.com/pkg/main.go:10.20,12.2 1 1
`
	os.WriteFile(coverFile, []byte(coverContent), 0644)

	mock := NewMockRunner()
	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"=== RUN TestFoo\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"output","Package":"example.com/pkg","Output":"coverage: 85.0% of statements\n"}
{"Time":"2024-01-01T00:00:03Z","Action":"pass","Package":"example.com/pkg"}
`
	mock.SetResponse("go", []string{"test", "-vet=off", "-json", "-coverprofile=" + coverFile, "./..."}, []byte(testOutput), nil)

	result, err := RunTests(mock, true, coverFile) // verbose=true
	if err != nil {
		t.Fatalf("runTests failed: %v", err)
	}

	if len(result.Coverage.Packages) != 1 {
		t.Errorf("expected 1 package, got %d", len(result.Coverage.Packages))
	}
}

func TestRunTestsNoCoverageFile(t *testing.T) {
	coverFile := filepath.Join(t.TempDir(), "coverage.out")
	// Don't create coverage.out - no profile means no statement-level data

	mock := NewMockRunner()
	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"pkg1"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"pkg1","Output":"coverage: 50.0% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"pkg1"}
{"Time":"2024-01-01T00:00:03Z","Action":"run","Package":"pkg2"}
{"Time":"2024-01-01T00:00:04Z","Action":"output","Package":"pkg2","Output":"coverage: 100% of statements\n"}
{"Time":"2024-01-01T00:00:05Z","Action":"pass","Package":"pkg2"}
`
	mock.SetResponse("go", []string{"test", "-vet=off", "-json", "-coverprofile=" + coverFile, "./..."}, []byte(testOutput), nil)

	result, err := RunTests(mock, false, coverFile)
	if err != nil {
		t.Fatalf("runTests failed: %v", err)
	}

	// Without a coverage profile we can't compute statement-weighted total.
	// Total should be 0 rather than a misleading per-package average.
	if result.Coverage.Total != 0 {
		t.Errorf("expected total 0 without coverage profile, got %v", result.Coverage.Total)
	}

	// Per-package percentages are still available from test output
	if len(result.Coverage.Packages) != 2 {
		t.Errorf("expected 2 packages, got %d", len(result.Coverage.Packages))
	}
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

	mock := NewMockRunner()
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
	mock.SetResponse("go", []string{"test", "-vet=off", "-json", "-coverprofile=" + coverFile, "./..."}, []byte(testOutput), nil)

	result, err := RunTests(mock, false, coverFile)
	if err != nil {
		t.Fatalf("runTests failed: %v", err)
	}

	// Total from profile: 3 covered / 4 statements = 75%
	// (statement-weighted, not per-package average)
	if result.Coverage.Total != 75.0 {
		t.Errorf("expected total 75.0 (statement-weighted), got %v", result.Coverage.Total)
	}

	// Verify statements are set correctly
	for _, p := range result.Coverage.Packages {
		switch p.Package {
		case "example.com/pkg1", "example.com/pkg2":
			if p.Statements == 0 {
				t.Errorf("package %s should have statements", p.Package)
			}
		case "example.com/pkg3":
			if p.Statements != 0 {
				t.Errorf("package %s should have no statements", p.Package)
			}
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

	mock := NewMockRunner()
	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg1"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg1","Output":"coverage: 50.0% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg1"}
{"Time":"2024-01-01T00:00:03Z","Action":"run","Package":"example.com/pkg2"}
{"Time":"2024-01-01T00:00:04Z","Action":"output","Package":"example.com/pkg2","Output":"coverage: [no statements]\n"}
{"Time":"2024-01-01T00:00:05Z","Action":"pass","Package":"example.com/pkg2"}
`
	mock.SetResponse("go", []string{"test", "-vet=off", "-json", "-coverprofile=" + coverFile, "./..."}, []byte(testOutput), nil)

	result, err := RunTests(mock, false, coverFile)
	if err != nil {
		t.Fatalf("runTests failed: %v", err)
	}

	// Total comes from ParseProfile: 1 covered / 2 statements = 50%
	if result.Coverage.Total != 50.0 {
		t.Errorf("expected total 50.0, got %v", result.Coverage.Total)
	}

	// Verify statements are set on the right package
	for _, p := range result.Coverage.Packages {
		if p.Package == "example.com/pkg2" && p.Statements != 0 {
			t.Error("pkg2 should have no statements")
		}
		if p.Package == "example.com/pkg1" && p.Statements == 0 {
			t.Error("pkg1 should have statements")
		}
	}
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

	mock := NewMockRunner()
	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg1"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg1","Output":"coverage: 100% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg1"}
{"Time":"2024-01-01T00:00:03Z","Action":"run","Package":"example.com/pkg2"}
{"Time":"2024-01-01T00:00:04Z","Action":"output","Package":"example.com/pkg2","Output":"coverage: 0% of statements\n"}
{"Time":"2024-01-01T00:00:05Z","Action":"pass","Package":"example.com/pkg2"}
`
	mock.SetResponse("go", []string{"test", "-vet=off", "-json", "-coverprofile=" + coverFile, "./..."}, []byte(testOutput), nil)

	result, err := RunTests(mock, false, coverFile)
	if err != nil {
		t.Fatalf("runTests failed: %v", err)
	}

	// Verify packages contain their files
	for _, p := range result.Coverage.Packages {
		switch p.Package {
		case "example.com/pkg1":
			if len(p.Files) != 2 {
				t.Errorf("pkg1: expected 2 files, got %d", len(p.Files))
			}
		case "example.com/pkg2":
			if len(p.Files) != 1 {
				t.Errorf("pkg2: expected 1 file, got %d", len(p.Files))
			}
		}
	}
}
