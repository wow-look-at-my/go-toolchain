package main

import (
	"fmt"
	"os"
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
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Create coverage.out
	coverContent := `mode: set
example.com/pkg/main.go:10.20,12.2 1 1
`
	os.WriteFile("coverage.out", []byte(coverContent), 0644)

	mock := NewMockRunner()
	// Return valid JSON test output
	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"coverage: 85.0% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg"}
`
	mock.SetResponse("go", []string{"test", "-json", "-coverprofile=coverage.out", "./..."}, []byte(testOutput), nil)

	result, err := runTests(mock, false)
	if err != nil {
		t.Fatalf("runTests failed: %v", err)
	}

	if len(result.Packages) != 1 {
		t.Errorf("expected 1 package, got %d", len(result.Packages))
	}

	if result.Packages[0].Coverage != 85.0 {
		t.Errorf("expected coverage 85.0, got %v", result.Packages[0].Coverage)
	}
}

func TestRunTestsFailure(t *testing.T) {
	mock := NewMockRunner()
	mock.SetResponse("go", []string{"test", "-json", "-coverprofile=coverage.out", "./..."}, nil, fmt.Errorf("test failed"))

	_, err := runTests(mock, false)
	if err == nil {
		t.Error("expected error when tests fail")
	}
}

func TestRunTestsVerbose(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	coverContent := `mode: set
example.com/pkg/main.go:10.20,12.2 1 1
`
	os.WriteFile("coverage.out", []byte(coverContent), 0644)

	mock := NewMockRunner()
	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"=== RUN TestFoo\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"output","Package":"example.com/pkg","Output":"coverage: 85.0% of statements\n"}
{"Time":"2024-01-01T00:00:03Z","Action":"pass","Package":"example.com/pkg"}
`
	mock.SetResponse("go", []string{"test", "-json", "-coverprofile=coverage.out", "./..."}, []byte(testOutput), nil)

	result, err := runTests(mock, true) // verbose=true
	if err != nil {
		t.Fatalf("runTests failed: %v", err)
	}

	if len(result.Packages) != 1 {
		t.Errorf("expected 1 package, got %d", len(result.Packages))
	}
}

func TestRunTestsNoCoverageFile(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Don't create coverage.out - should fallback to averaging

	mock := NewMockRunner()
	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"pkg1"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"pkg1","Output":"coverage: 50.0% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"pkg1"}
{"Time":"2024-01-01T00:00:03Z","Action":"run","Package":"pkg2"}
{"Time":"2024-01-01T00:00:04Z","Action":"output","Package":"pkg2","Output":"coverage: 100% of statements\n"}
{"Time":"2024-01-01T00:00:05Z","Action":"pass","Package":"pkg2"}
`
	mock.SetResponse("go", []string{"test", "-json", "-coverprofile=coverage.out", "./..."}, []byte(testOutput), nil)

	result, err := runTests(mock, false)
	if err != nil {
		t.Fatalf("runTests failed: %v", err)
	}

	// Should average: (50 + 100) / 2 = 75
	if result.Total != 75.0 {
		t.Errorf("expected total 75.0, got %v", result.Total)
	}
}
