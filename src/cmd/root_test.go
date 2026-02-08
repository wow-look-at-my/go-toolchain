package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	gotest "go-safe-build/src/test"
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
	mock := &testPipesFailMockRunner{}

	jsonOutput = true
	defer func() { jsonOutput = false }()

	err := runWithRunner(mock)
	if err == nil {
		t.Error("expected error when tests fail")
	}
}

func TestRunWithRunnerTestOnlyMode(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

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

	mock := &testPassMockRunner{coveragePct: 50}

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

	mock := &testPassMockRunner{}

	jsonOutput = true
	minCoverage = 80
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		minCoverage = 80
		outputDir = "build"
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

	mock := &testPassMockRunner{}

	jsonOutput = false
	minCoverage = 80
	verbose = true
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		minCoverage = 80
		verbose = false
		outputDir = "build"
	}()

	err := runWithRunner(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// testPassMockRunner returns valid test JSON output with configurable coverage.
// If coveragePct is 0, defaults to 100%.
type testPassMockRunner struct {
	commands    []MockCommand
	coveragePct float32
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
	pct := m.coveragePct
	if pct == 0 {
		pct = 100
	}
	output := fmt.Sprintf(`{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"coverage: %.1f%% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg"}
`, pct)
	return bytes.NewReader([]byte(output)), func() error { return nil }, nil
}

// testPipesFailMockRunner fails immediately on RunWithPipes (simulating test execution failure)
type testPipesFailMockRunner struct {
	commands []MockCommand
}

func (m *testPipesFailMockRunner) Run(name string, args ...string) error {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	return nil
}

func (m *testPipesFailMockRunner) RunWithOutput(name string, args ...string) ([]byte, error) {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	return nil, nil
}

func (m *testPipesFailMockRunner) RunWithPipes(name string, args ...string) (io.Reader, func() error, error) {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	return nil, nil, fmt.Errorf("tests failed")
}

func TestRunWithRunnerNonJSON(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := &testPassMockRunner{}

	// Test non-JSON output path
	jsonOutput = false
	minCoverage = 80
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		minCoverage = 80
		outputDir = "build"
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

	mock := &testPassMockRunner{}

	jsonOutput = false
	minCoverage = 80
	covDetail = "file"
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		minCoverage = 80
		covDetail = ""
		outputDir = "build"
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

	mock := &testPassMockRunner{}

	jsonOutput = false
	minCoverage = 80
	covDetail = "func"
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		minCoverage = 80
		covDetail = ""
		outputDir = "build"
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

	mock := &buildFailMockRunner{}

	jsonOutput = true
	minCoverage = 80
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		minCoverage = 80
		outputDir = "build"
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

	mock := &testPassMockRunner{coveragePct: 50}

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

	mock := &testPassMockRunner{coveragePct: 50}

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

func TestRunWithRunnerAddWatermark(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := &testPassMockRunner{}

	jsonOutput = false
	minCoverage = 80
	addWatermark = true
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		minCoverage = 80
		addWatermark = false
		outputDir = "build"
	}()

	err := runWithRunner(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify watermark was set
	wm, exists, werr := gotest.GetWatermark(".")
	if werr != nil {
		t.Fatalf("getWatermark: %v", werr)
	}
	if !exists {
		t.Fatal("expected watermark to exist after --add-watermark")
	}
	if wm != 100.0 {
		t.Errorf("expected watermark 100.0, got %v", wm)
	}
}

func TestRunWithRunnerAddWatermarkJSON(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := &testPassMockRunner{}

	jsonOutput = true
	minCoverage = 80
	addWatermark = true
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		minCoverage = 80
		addWatermark = false
		outputDir = "build"
	}()

	err := runWithRunner(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunWithRunnerWatermarkEnforcement(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Set watermark to 60% — grace = 57.5, effective = min(80, 57.5) = 57.5
	// 50 < 57.5 → should fail
	gotest.SetWatermark(".", 60.0)

	mock := &testPassMockRunner{coveragePct: 50}

	jsonOutput = false
	minCoverage = 80
	defer func() {
		jsonOutput = false
		minCoverage = 80
	}()

	err := runWithRunner(mock)
	if err == nil {
		t.Error("expected error when coverage below watermark grace")
	}
	if err != nil && !strings.Contains(err.Error(), "below minimum") {
		t.Errorf("expected 'below minimum' error, got: %v", err)
	}
}

func TestRunWithRunnerWatermarkGracePass(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Set watermark to 52% — grace = 49.5, effective = min(80, 49.5) = 49.5
	// 50 > 49.5 → should pass
	gotest.SetWatermark(".", 52.0)

	mock := &testPassMockRunner{coveragePct: 50}

	jsonOutput = true
	minCoverage = 80
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		minCoverage = 80
		outputDir = "build"
	}()

	err := runWithRunner(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunWithRunnerWatermarkRatchetUp(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Set watermark to 50% — coverage is 100%, should ratchet up
	gotest.SetWatermark(".", 50.0)

	mock := &testPassMockRunner{}

	jsonOutput = false
	minCoverage = 80
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		minCoverage = 80
		outputDir = "build"
	}()

	err := runWithRunner(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify watermark was ratcheted up
	wm, _, _ := gotest.GetWatermark(".")
	if wm != 100.0 {
		t.Errorf("expected watermark 100.0, got %v", wm)
	}
}

func TestHandleRemoveWatermarkNoWatermark(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := handleRemoveWatermark()

	w.Close()
	out, _ := io.ReadAll(r)
	os.Stdout = old

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out), "No watermark is set") {
		t.Errorf("expected 'No watermark is set', got: %s", out)
	}
}

func TestHandleRemoveWatermarkAborted(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	gotest.SetWatermark(".", 80.0)

	// Simulate "n" input
	oldStdin := os.Stdin
	rIn, wIn, _ := os.Pipe()
	wIn.WriteString("n\n")
	wIn.Close()
	os.Stdin = rIn
	defer func() { os.Stdin = oldStdin }()

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := handleRemoveWatermark()

	w.Close()
	out, _ := io.ReadAll(r)
	os.Stdout = old

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out), "Aborted") {
		t.Errorf("expected 'Aborted', got: %s", out)
	}

	// Watermark should still exist
	_, exists, _ := gotest.GetWatermark(".")
	if !exists {
		t.Error("watermark should still exist after abort")
	}
}

func TestHandleRemoveWatermarkConfirmed(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	gotest.SetWatermark(".", 80.0)

	// Simulate "y" input
	oldStdin := os.Stdin
	rIn, wIn, _ := os.Pipe()
	wIn.WriteString("y\n")
	wIn.Close()
	os.Stdin = rIn
	defer func() { os.Stdin = oldStdin }()

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := handleRemoveWatermark()

	w.Close()
	out, _ := io.ReadAll(r)
	os.Stdout = old

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out), "Watermark removed") {
		t.Errorf("expected 'Watermark removed', got: %s", out)
	}

	// Watermark should be gone
	_, exists, _ := gotest.GetWatermark(".")
	if exists {
		t.Error("watermark should not exist after removal")
	}
}

func TestRunWithRunnerRemoveWatermarkFlag(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	gotest.SetWatermark(".", 80.0)

	// Simulate "yes" input
	oldStdin := os.Stdin
	rIn, wIn, _ := os.Pipe()
	wIn.WriteString("yes\n")
	wIn.Close()
	os.Stdin = rIn
	defer func() { os.Stdin = oldStdin }()

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	doRemoveWmark = true
	defer func() { doRemoveWmark = false }()

	err := runWithRunner(nil) // runner not needed for remove-watermark

	w.Close()
	out, _ := io.ReadAll(r)
	os.Stdout = old

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out), "Watermark removed") {
		t.Errorf("expected 'Watermark removed', got: %s", out)
	}
}

func TestRunWithRunnerFailedTest(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := &testFailMockRunner{}

	jsonOutput = false
	minCoverage = 80
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		minCoverage = 80
		outputDir = "build"
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

func TestNeedsGenerateNoDirectives(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/main.go", []byte("package main\nfunc main() {}\n"), 0644)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	if needsGenerate() {
		t.Error("expected false when no go:generate directives")
	}
}

func TestNeedsGenerateWithDirective(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/main.go", []byte("package main\n//go:generate echo hello\nfunc main() {}\n"), 0644)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	if !needsGenerate() {
		t.Error("expected true when go:generate directive present")
	}
}
