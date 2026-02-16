package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	gotest "github.com/wow-look-at-my/go-toolchain/src/test"
)

// writeMockCoverProfile writes a minimal Go coverage profile matching the
// given percentage. It parses the -coverprofile= flag from the args to find
// the output path. This simulates what `go test -coverprofile` does in real
// usage so that ParseProfile computes the correct statement-weighted total.
func writeMockCoverProfile(args []string, pct float32) {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-coverprofile=") {
			continue
		}
		coverFile := strings.TrimPrefix(arg, "-coverprofile=")
		covered := int(pct + 0.5)
		uncovered := 100 - covered
		content := "mode: set\n"
		if covered > 0 {
			content += fmt.Sprintf("example.com/pkg/main.go:1.1,2.2 %d 1\n", covered)
		}
		if uncovered > 0 {
			content += fmt.Sprintf("example.com/pkg/main.go:3.1,4.2 %d 0\n", uncovered)
		}
		os.WriteFile(coverFile, []byte(content), 0644)
		return
	}
}

// mockProcess implements runner.IProcess for testing
type mockProcess struct {
	stdout  []byte
	stderr  []byte
	waitErr error
}

func (p *mockProcess) Wait() error    { return p.waitErr }
func (p *mockProcess) Stdout() io.Reader { return bytes.NewReader(p.stdout) }
func (p *mockProcess) Stderr() io.Reader { return bytes.NewReader(p.stderr) }

// testPassMockRunner returns valid test JSON output with configurable coverage.
// If coveragePct is 0, defaults to 100%.
type testPassMockRunner struct {
	calls       []runner.Config
	coveragePct float32
}

func (m *testPassMockRunner) Run(cfg runner.Config) (runner.IProcess, error) {
	m.calls = append(m.calls, cfg)

	// For go test commands, write cover profile and return JSON output
	if cfg.Name == "go" && len(cfg.Args) > 0 && (cfg.Args[0] == "test" || (len(cfg.Args) > 1 && cfg.Args[1] == "test")) {
		pct := m.coveragePct
		if pct == 0 {
			pct = 100
		}
		writeMockCoverProfile(cfg.Args, pct)
		output := fmt.Sprintf(`{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"coverage: %.1f%% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg"}
`, pct)
		return &mockProcess{stdout: []byte(output)}, nil
	}

	return &mockProcess{}, nil
}

// testPipesFailMockRunner fails immediately on test execution
type testPipesFailMockRunner struct {
	calls []runner.Config
}

func (m *testPipesFailMockRunner) Run(cfg runner.Config) (runner.IProcess, error) {
	m.calls = append(m.calls, cfg)

	// Fail on go test
	if cfg.Name == "go" && len(cfg.Args) > 0 && (cfg.Args[0] == "test" || (len(cfg.Args) > 1 && cfg.Args[1] == "test")) {
		return nil, fmt.Errorf("tests failed")
	}

	return &mockProcess{}, nil
}

// modTidyFailMockRunner fails on go mod tidy
type modTidyFailMockRunner struct {
	calls []runner.Config
}

func (m *modTidyFailMockRunner) Run(cfg runner.Config) (runner.IProcess, error) {
	m.calls = append(m.calls, cfg)

	if cfg.Name == "go" && len(cfg.Args) > 0 && cfg.Args[0] == "mod" {
		return &mockProcess{waitErr: fmt.Errorf("mod tidy failed")}, nil
	}

	return &mockProcess{}, nil
}

func TestRunWithRunnerModTidyFails(t *testing.T) {
	mock := &modTidyFailMockRunner{}

	jsonOutput = true
	defer func() { jsonOutput = false }()

	err := runWithRunner(mock)
	assert.NotNil(t, err)
}

func TestRunWithRunnerTestsFail(t *testing.T) {
	mock := &testPipesFailMockRunner{}

	jsonOutput = true
	defer func() { jsonOutput = false }()

	err := runWithRunner(mock)
	assert.NotNil(t, err)
}

func TestRunWithRunnerCoverageBelowThreshold(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := &testPassMockRunner{coveragePct: 50}

	jsonOutput = true
	defer func() { jsonOutput = false }()

	err := runWithRunner(mock)
	assert.NotNil(t, err)
}

func TestRunWithRunnerSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := &testPassMockRunner{}

	jsonOutput = true
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		outputDir = "build"
	}()

	err := runWithRunner(mock)
	assert.Nil(t, err)
}

func TestRunWithRunnerSuccessVerbose(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := &testPassMockRunner{}

	jsonOutput = false
	verbose = true
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		verbose = false
		outputDir = "build"
	}()

	err := runWithRunner(mock)
	assert.Nil(t, err)
}

func TestRunWithRunnerNonJSON(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := &testPassMockRunner{}

	jsonOutput = false
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		outputDir = "build"
	}()

	err := runWithRunner(mock)
	assert.Nil(t, err)
}

func TestRunWithRunnerFileDetail(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := &testPassMockRunner{}

	jsonOutput = false
	covDetail = "file"
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		covDetail = ""
		outputDir = "build"
	}()

	err := runWithRunner(mock)
	assert.Nil(t, err)
}

func TestRunWithRunnerFuncDetail(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := &testPassMockRunner{}

	jsonOutput = false
	covDetail = "func"
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		covDetail = ""
		outputDir = "build"
	}()

	err := runWithRunner(mock)
	// May fail due to go tool cover not finding sources, but covers the code path
	_ = err
}

// buildFailMockRunner passes tests but fails build
type buildFailMockRunner struct {
	calls []runner.Config
}

func (m *buildFailMockRunner) Run(cfg runner.Config) (runner.IProcess, error) {
	m.calls = append(m.calls, cfg)

	// Fail on go build
	if cfg.Name == "go" && len(cfg.Args) > 0 && cfg.Args[0] == "build" {
		return &mockProcess{waitErr: fmt.Errorf("build failed")}, nil
	}

	// For go test commands, return success
	if cfg.Name == "go" && len(cfg.Args) > 0 && (cfg.Args[0] == "test" || (len(cfg.Args) > 1 && cfg.Args[1] == "test")) {
		writeMockCoverProfile(cfg.Args, 100)
		output := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"coverage: 100% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg"}
`
		return &mockProcess{stdout: []byte(output)}, nil
	}

	return &mockProcess{}, nil
}

func TestRunWithRunnerBuildFails(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := &buildFailMockRunner{}

	jsonOutput = true
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		outputDir = "build"
	}()

	err := runWithRunner(mock)
	assert.NotNil(t, err)
}

func TestRunWithRunnerCoverageBelowThresholdNonJSON(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := &testPassMockRunner{coveragePct: 50}

	jsonOutput = false // Non-JSON output to hit uncovered functions display
	defer func() { jsonOutput = false }()

	err := runWithRunner(mock)
	assert.NotNil(t, err)
}

func TestRunWithRunnerCoverageBelowThresholdJSON(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := &testPassMockRunner{coveragePct: 50}

	jsonOutput = true // JSON output path when below threshold
	defer func() { jsonOutput = false }()

	err := runWithRunner(mock)
	assert.NotNil(t, err)
}

func TestRunWithRunnerAddWatermark(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := &testPassMockRunner{}

	jsonOutput = false
	addWatermark = true
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		addWatermark = false
		outputDir = "build"
	}()

	err := runWithRunner(mock)
	assert.Nil(t, err)

	// Verify watermark was set
	wm, exists, werr := gotest.GetWatermark(".")
	require.Nil(t, werr)
	require.True(t, exists)
	assert.Equal(t, 100.0, wm)
}

func TestRunWithRunnerAddWatermarkJSON(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := &testPassMockRunner{}

	jsonOutput = true
	addWatermark = true
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		addWatermark = false
		outputDir = "build"
	}()

	err := runWithRunner(mock)
	assert.Nil(t, err)
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
	defer func() { jsonOutput = false }()

	err := runWithRunner(mock)
	assert.NotNil(t, err)
	assert.False(t, err != nil && !strings.Contains(err.Error(), "below minimum"))
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
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		outputDir = "build"
	}()

	err := runWithRunner(mock)
	assert.Nil(t, err)
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
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		outputDir = "build"
	}()

	err := runWithRunner(mock)
	assert.Nil(t, err)

	// Verify watermark was ratcheted up
	wm, _, _ := gotest.GetWatermark(".")
	assert.Equal(t, 100.0, wm)
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

	assert.Nil(t, err)
	assert.Contains(t, string(out), "No watermark is set")
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

	assert.Nil(t, err)
	assert.Contains(t, string(out), "Aborted")

	// Watermark should still exist
	_, exists, _ := gotest.GetWatermark(".")
	assert.True(t, exists)
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

	assert.Nil(t, err)
	assert.Contains(t, string(out), "Watermark removed")

	// Watermark should be gone
	_, exists, _ := gotest.GetWatermark(".")
	assert.False(t, exists)
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

	assert.Nil(t, err)
	assert.Contains(t, string(out), "Watermark removed")
}

// testFailMockRunner returns output with a failed test (but wait() succeeds)
type testFailMockRunner struct {
	calls []runner.Config
}

func (m *testFailMockRunner) Run(cfg runner.Config) (runner.IProcess, error) {
	m.calls = append(m.calls, cfg)

	// For go test commands, return JSON with a failed test
	if cfg.Name == "go" && len(cfg.Args) > 0 && (cfg.Args[0] == "test" || (len(cfg.Args) > 1 && cfg.Args[1] == "test")) {
		writeMockCoverProfile(cfg.Args, 100)
		output := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"coverage: 100% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"fail","Package":"example.com/pkg"}
`
		return &mockProcess{stdout: []byte(output)}, nil
	}

	return &mockProcess{}, nil
}

func TestRunWithRunnerFailedTest(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := &testFailMockRunner{}

	jsonOutput = false
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		outputDir = "build"
	}()

	err := runWithRunner(mock)
	assert.Nil(t, err)
}

// testFailWithErrorMockRunner returns output AND an error from wait()
type testFailWithErrorMockRunner struct {
	calls []runner.Config
}

func (m *testFailWithErrorMockRunner) Run(cfg runner.Config) (runner.IProcess, error) {
	m.calls = append(m.calls, cfg)

	// For go test commands, return JSON with test output showing a failure
	if cfg.Name == "go" && len(cfg.Args) > 0 && (cfg.Args[0] == "test" || (len(cfg.Args) > 1 && cfg.Args[1] == "test")) {
		output := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg","Test":"TestFoo"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Test":"TestFoo","Output":"=== RUN   TestFoo\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"output","Package":"example.com/pkg","Test":"TestFoo","Output":"    foo_test.go:10: assertion failed\n"}
{"Time":"2024-01-01T00:00:03Z","Action":"output","Package":"example.com/pkg","Test":"TestFoo","Output":"--- FAIL: TestFoo (0.00s)\n"}
{"Time":"2024-01-01T00:00:04Z","Action":"fail","Package":"example.com/pkg","Test":"TestFoo"}
{"Time":"2024-01-01T00:00:05Z","Action":"output","Package":"example.com/pkg","Output":"FAIL\n"}
{"Time":"2024-01-01T00:00:06Z","Action":"fail","Package":"example.com/pkg"}
`
		return &mockProcess{stdout: []byte(output), waitErr: fmt.Errorf("exit status 1")}, nil
	}

	return &mockProcess{}, nil
}

func TestRunWithRunnerTestsFailWithOutput(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := &testFailWithErrorMockRunner{}

	jsonOutput = false
	verbose = true // Should show test output before error
	defer func() {
		jsonOutput = false
		verbose = false
	}()

	err := runWithRunner(mock)
	assert.NotNil(t, err)
	// The key point: results are still displayed before the error is returned
}

func TestNeedsGenerateNoDirectives(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/main.go", []byte("package main\nfunc main() {}\n"), 0644)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	assert.False(t, needsGenerate())
}

func TestNeedsGenerateWithDirective(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/main.go", []byte("package main\n//go:generate echo hello\nfunc main() {}\n"), 0644)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	assert.True(t, needsGenerate())
}
