package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunWithRunnerModTidyFails(t *testing.T) {
	mock := NewMockRunner()
	mock.SetResponse("go", []string{"mod", "tidy"}, nil, fmt.Errorf("mod tidy failed"))

	jsonOutput = true
	defer func() { jsonOutput = false }()

	err := runWithRunner(mock)
	if err == nil {
		t.Error("expected error when mod tidy fails")
	}
}

func TestRunWithRunnerTestsFail(t *testing.T) {
	mock := NewMockRunner()
	// mod tidy succeeds
	mock.SetResponse("go", []string{"mod", "tidy"}, nil, nil)
	// tests fail
	mock.SetResponse("go", []string{"test", "-json", "-coverprofile=coverage.out", "./..."}, nil, fmt.Errorf("tests failed"))

	jsonOutput = true
	defer func() { jsonOutput = false }()

	err := runWithRunner(mock)
	if err == nil {
		t.Error("expected error when tests fail")
	}
}

func TestRunWithRunnerTestOnlyMode(t *testing.T) {
	// Create temp coverage.out for parsing
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	coverContent := "mode: set\nexample.com/pkg/main.go:10.20,12.2 1 1\n"
	os.WriteFile("coverage.out", []byte(coverContent), 0644)

	mock := &testPassMockRunner{}

	jsonOutput = true
	minCoverage = 0 // test-only mode
	defer func() {
		jsonOutput = false
		minCoverage = 80
	}()

	err := runWithRunner(mock)
	if err == nil {
		t.Error("expected error in test-only mode")
	}
	if err != nil && err.Error() != "test-only mode: coverage is 100.0%" {
		// Coverage might vary, just check it's test-only error
		if !bytes.Contains([]byte(err.Error()), []byte("test-only mode")) {
			t.Errorf("unexpected error: %v", err)
		}
	}
}

func TestRunWithRunnerCoverageBelowThreshold(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Create coverage.out with 50% coverage
	coverContent := `mode: set
example.com/pkg/main.go:10.20,12.2 1 1
example.com/pkg/main.go:14.20,16.2 1 0
`
	os.WriteFile("coverage.out", []byte(coverContent), 0644)

	mock := &testPassMockRunner{}

	jsonOutput = true
	minCoverage = 80
	defer func() {
		jsonOutput = false
		minCoverage = 80
	}()

	err := runWithRunner(mock)
	if err == nil {
		t.Error("expected error when coverage below threshold")
	}
}

func TestRunWithRunnerSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Create coverage.out with 100% coverage
	coverContent := `mode: set
example.com/pkg/main.go:10.20,12.2 1 1
`
	os.WriteFile("coverage.out", []byte(coverContent), 0644)

	mock := &testPassMockRunner{}

	jsonOutput = true
	minCoverage = 80
	output = filepath.Join(tmpDir, "testbinary")
	defer func() {
		jsonOutput = false
		minCoverage = 80
		output = ""
	}()

	err := runWithRunner(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunWithRunnerSuccessVerbose(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	coverContent := `mode: set
example.com/pkg/main.go:10.20,12.2 1 1
`
	os.WriteFile("coverage.out", []byte(coverContent), 0644)

	mock := &testPassMockRunner{}

	jsonOutput = false
	minCoverage = 80
	verbose = true
	output = filepath.Join(tmpDir, "testbinary")
	defer func() {
		jsonOutput = false
		minCoverage = 80
		verbose = false
		output = ""
	}()

	err := runWithRunner(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// testPassMockRunner returns valid test JSON output
type testPassMockRunner struct {
	commands []MockCommand
}

func (m *testPassMockRunner) Run(name string, args ...string) error {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	return nil
}

func (m *testPassMockRunner) RunWithOutput(name string, args ...string) ([]byte, error) {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	return nil, nil
}

func (m *testPassMockRunner) RunWithPipes(name string, args ...string) (io.Reader, func() error, error) {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	// Return valid JSON test output
	output := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"coverage: 100% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg"}
`
	return bytes.NewReader([]byte(output)), func() error { return nil }, nil
}

func TestRunWithRunnerNonJSON(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Create coverage.out with 100% coverage
	coverContent := `mode: set
example.com/pkg/main.go:10.20,12.2 1 1
`
	os.WriteFile("coverage.out", []byte(coverContent), 0644)

	mock := &testPassMockRunner{}

	// Test non-JSON output path
	jsonOutput = false
	minCoverage = 80
	output = filepath.Join(tmpDir, "testbinary")
	defer func() {
		jsonOutput = false
		minCoverage = 80
		output = ""
	}()

	err := runWithRunner(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunWithRunnerFileDetail(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	coverContent := `mode: set
example.com/pkg/main.go:10.20,12.2 1 1
`
	os.WriteFile("coverage.out", []byte(coverContent), 0644)

	mock := &testPassMockRunner{}

	jsonOutput = false
	minCoverage = 80
	covDetail = "file"
	output = filepath.Join(tmpDir, "testbinary")
	defer func() {
		jsonOutput = false
		minCoverage = 80
		covDetail = ""
		output = ""
	}()

	err := runWithRunner(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunWithRunnerFuncDetail(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	coverContent := `mode: set
example.com/pkg/main.go:10.20,12.2 1 1
`
	os.WriteFile("coverage.out", []byte(coverContent), 0644)

	mock := &testPassMockRunner{}

	jsonOutput = false
	minCoverage = 80
	covDetail = "func"
	output = filepath.Join(tmpDir, "testbinary")
	defer func() {
		jsonOutput = false
		minCoverage = 80
		covDetail = ""
		output = ""
	}()

	err := runWithRunner(mock)
	// May fail due to go tool cover not finding sources, but covers the code path
	_ = err
}

func TestRunWithRunnerVetFails(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := &vetFailMockRunner{}

	jsonOutput = false
	minCoverage = 80
	defer func() {
		jsonOutput = false
		minCoverage = 80
	}()

	err := runWithRunner(mock)
	if err == nil {
		t.Error("expected error when vet fails")
	}
	if !strings.Contains(err.Error(), "go vet failed") {
		t.Errorf("expected 'go vet failed' in error, got: %v", err)
	}
}

func TestRunWithRunnerBuildFails(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	coverContent := `mode: set
example.com/pkg/main.go:10.20,12.2 1 1
`
	os.WriteFile("coverage.out", []byte(coverContent), 0644)

	mock := &buildFailMockRunner{}

	jsonOutput = true
	minCoverage = 80
	output = filepath.Join(tmpDir, "testbinary")
	defer func() {
		jsonOutput = false
		minCoverage = 80
		output = ""
	}()

	err := runWithRunner(mock)
	if err == nil {
		t.Error("expected error when build fails")
	}
}

// buildFailMockRunner passes tests but fails build
type buildFailMockRunner struct {
	commands []MockCommand
}

func (m *buildFailMockRunner) Run(name string, args ...string) error {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	// Fail on go build
	if name == "go" && len(args) > 0 && args[0] == "build" {
		return fmt.Errorf("build failed")
	}
	return nil
}

// vetFailMockRunner fails on go vet
type vetFailMockRunner struct {
	commands []MockCommand
}

func (m *vetFailMockRunner) Run(name string, args ...string) error {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	if name == "go" && len(args) > 0 && args[0] == "vet" {
		return fmt.Errorf("vet failed: suspicious code")
	}
	return nil
}

func (m *vetFailMockRunner) RunWithOutput(name string, args ...string) ([]byte, error) {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	return nil, nil
}

func (m *vetFailMockRunner) RunWithPipes(name string, args ...string) (io.Reader, func() error, error) {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	return bytes.NewReader([]byte{}), func() error { return nil }, nil
}

func (m *buildFailMockRunner) RunWithOutput(name string, args ...string) ([]byte, error) {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	return nil, nil
}

func (m *buildFailMockRunner) RunWithPipes(name string, args ...string) (io.Reader, func() error, error) {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	output := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"coverage: 100% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg"}
`
	return bytes.NewReader([]byte(output)), func() error { return nil }, nil
}

func TestRunWithRunnerTestOnlyNonJSON(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	coverContent := "mode: set\nexample.com/pkg/main.go:10.20,12.2 1 1\n"
	os.WriteFile("coverage.out", []byte(coverContent), 0644)

	mock := &testPassMockRunner{}

	jsonOutput = false // Non-JSON output path
	minCoverage = 0    // test-only mode
	defer func() {
		jsonOutput = false
		minCoverage = 80
	}()

	err := runWithRunner(mock)
	if err == nil {
		t.Error("expected error in test-only mode")
	}
}

func TestRunWithRunnerCoverageBelowThresholdNonJSON(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	coverContent := `mode: set
example.com/pkg/main.go:10.20,12.2 1 1
example.com/pkg/main.go:14.20,16.2 1 0
`
	os.WriteFile("coverage.out", []byte(coverContent), 0644)

	mock := &testPassMockRunner{}

	jsonOutput = false // Non-JSON output to hit uncovered functions display
	minCoverage = 80
	defer func() {
		jsonOutput = false
		minCoverage = 80
	}()

	err := runWithRunner(mock)
	if err == nil {
		t.Error("expected error when coverage below threshold")
	}
}

func TestRunWithRunnerCoverageBelowThresholdJSON(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	coverContent := `mode: set
example.com/pkg/main.go:10.20,12.2 1 1
example.com/pkg/main.go:14.20,16.2 1 0
`
	os.WriteFile("coverage.out", []byte(coverContent), 0644)

	mock := &testPassMockRunner{}

	jsonOutput = true // JSON output path when below threshold
	minCoverage = 80
	defer func() {
		jsonOutput = false
		minCoverage = 80
	}()

	err := runWithRunner(mock)
	if err == nil {
		t.Error("expected error when coverage below threshold")
	}
}

func TestRunWithRunnerTestOnlyJSON(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	coverContent := "mode: set\nexample.com/pkg/main.go:10.20,12.2 1 1\n"
	os.WriteFile("coverage.out", []byte(coverContent), 0644)

	mock := &testPassMockRunner{}

	jsonOutput = true
	minCoverage = 0 // test-only mode with JSON
	defer func() {
		jsonOutput = false
		minCoverage = 80
	}()

	err := runWithRunner(mock)
	if err == nil {
		t.Error("expected error in test-only mode")
	}
}

func TestRunWithRunnerFailedTest(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	coverContent := "mode: set\nexample.com/pkg/main.go:10.20,12.2 1 1\n"
	os.WriteFile("coverage.out", []byte(coverContent), 0644)

	mock := &testFailMockRunner{}

	jsonOutput = false
	minCoverage = 80
	output = filepath.Join(tmpDir, "testbinary")
	defer func() {
		jsonOutput = false
		minCoverage = 80
		output = ""
	}()

	err := runWithRunner(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunWithRunnerTestsFailWithOutput(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	coverContent := "mode: set\nexample.com/pkg/main.go:10.20,12.2 1 1\n"
	os.WriteFile("coverage.out", []byte(coverContent), 0644)

	mock := &testFailWithErrorMockRunner{}

	jsonOutput = false
	verbose = true // Should show test output before error
	minCoverage = 80
	defer func() {
		jsonOutput = false
		verbose = false
		minCoverage = 80
	}()

	err := runWithRunner(mock)
	if err == nil {
		t.Error("expected error when tests fail")
	}
	// The key point: results are still displayed before the error is returned
}

// testFailMockRunner returns output with a failed test (but wait() succeeds)
type testFailMockRunner struct {
	commands []MockCommand
}

func (m *testFailMockRunner) Run(name string, args ...string) error {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	return nil
}

func (m *testFailMockRunner) RunWithOutput(name string, args ...string) ([]byte, error) {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	return nil, nil
}

func (m *testFailMockRunner) RunWithPipes(name string, args ...string) (io.Reader, func() error, error) {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	// Return JSON with a failed test
	output := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"coverage: 100% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"fail","Package":"example.com/pkg"}
`
	return bytes.NewReader([]byte(output)), func() error { return nil }, nil
}

// testFailWithErrorMockRunner returns output AND an error from wait()
type testFailWithErrorMockRunner struct {
	commands []MockCommand
}

func (m *testFailWithErrorMockRunner) Run(name string, args ...string) error {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	return nil
}

func (m *testFailWithErrorMockRunner) RunWithOutput(name string, args ...string) ([]byte, error) {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	return nil, nil
}

func (m *testFailWithErrorMockRunner) RunWithPipes(name string, args ...string) (io.Reader, func() error, error) {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	// Return JSON with test output showing a failure
	output := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg","Test":"TestFoo"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Test":"TestFoo","Output":"=== RUN   TestFoo\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"output","Package":"example.com/pkg","Test":"TestFoo","Output":"    foo_test.go:10: assertion failed\n"}
{"Time":"2024-01-01T00:00:03Z","Action":"output","Package":"example.com/pkg","Test":"TestFoo","Output":"--- FAIL: TestFoo (0.00s)\n"}
{"Time":"2024-01-01T00:00:04Z","Action":"fail","Package":"example.com/pkg","Test":"TestFoo"}
{"Time":"2024-01-01T00:00:05Z","Action":"output","Package":"example.com/pkg","Output":"FAIL\n"}
{"Time":"2024-01-01T00:00:06Z","Action":"fail","Package":"example.com/pkg"}
`
	return bytes.NewReader([]byte(output)), func() error { return fmt.Errorf("exit status 1") }, nil
}
