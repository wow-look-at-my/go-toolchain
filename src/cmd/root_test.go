package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
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

// handleGoList handles go list commands for mocks, returning fake main package info.
func handleGoList(cfg runner.Config) (runner.IProcess, bool) {
	if !cfg.IsCmd("go", "list") {
		return nil, false
	}
	for _, arg := range cfg.Args {
		if strings.Contains(arg, "main") {
			return runner.MockProcess([]byte("example.com/pkg\n"), nil), true
		}
		if arg == "-m" {
			return runner.MockProcess([]byte("example.com\n"), nil), true
		}
	}
	return nil, false
}

// newTestPassMock creates a mock runner that passes tests with the given coverage percentage.
// If pct is 0, it defaults to 100%.
func newTestPassMock(pct float32) *runner.Mock {
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "test") {
			covPct := pct
			if covPct == 0 {
				covPct = 100
			}
			writeMockCoverProfile(cfg.Args, covPct)
			output := fmt.Sprintf(`{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"coverage: %.1f%% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg"}
`, covPct)
			return runner.MockProcess([]byte(output), nil), nil
		}
		if proc, ok := handleGoList(cfg); ok {
			return proc, nil
		}
		return nil, nil // fall through to default
	}
	return mock
}

// newTestPipesFailMock creates a mock runner that returns an error when running tests.
func newTestPipesFailMock() *runner.Mock {
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "test") {
			return nil, fmt.Errorf("tests failed")
		}
		if proc, ok := handleGoList(cfg); ok {
			return proc, nil
		}
		return nil, nil
	}
	return mock
}

// newModTidyFailMock creates a mock runner that fails on go mod tidy.
func newModTidyFailMock() *runner.Mock {
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "mod") {
			return runner.MockProcess(nil, fmt.Errorf("mod tidy failed")), nil
		}
		if proc, ok := handleGoList(cfg); ok {
			return proc, nil
		}
		return nil, nil
	}
	return mock
}

// newBuildFailMock creates a mock runner that passes tests but fails on go build.
func newBuildFailMock() *runner.Mock {
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "build") {
			return runner.MockProcess(nil, fmt.Errorf("build failed")), nil
		}
		if cfg.IsCmd("go", "test") {
			writeMockCoverProfile(cfg.Args, 100)
			output := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"coverage: 100% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg"}
`
			return runner.MockProcess([]byte(output), nil), nil
		}
		if proc, ok := handleGoList(cfg); ok {
			return proc, nil
		}
		return nil, nil
	}
	return mock
}

// newTestFailMock creates a mock runner that returns output with a failed test (but wait() succeeds).
func newTestFailMock() *runner.Mock {
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "test") {
			writeMockCoverProfile(cfg.Args, 100)
			output := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"coverage: 100% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"fail","Package":"example.com/pkg"}
`
			return runner.MockProcess([]byte(output), nil), nil
		}
		if proc, ok := handleGoList(cfg); ok {
			return proc, nil
		}
		return nil, nil
	}
	return mock
}

// newTestFailWithErrorMock creates a mock runner that returns output AND an error from wait().
func newTestFailWithErrorMock() *runner.Mock {
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "test") {
			output := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg","Test":"TestFoo"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Test":"TestFoo","Output":"=== RUN   TestFoo\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"output","Package":"example.com/pkg","Test":"TestFoo","Output":"    foo_test.go:10: assertion failed\n"}
{"Time":"2024-01-01T00:00:03Z","Action":"output","Package":"example.com/pkg","Test":"TestFoo","Output":"--- FAIL: TestFoo (0.00s)\n"}
{"Time":"2024-01-01T00:00:04Z","Action":"fail","Package":"example.com/pkg","Test":"TestFoo"}
{"Time":"2024-01-01T00:00:05Z","Action":"output","Package":"example.com/pkg","Output":"FAIL\n"}
{"Time":"2024-01-01T00:00:06Z","Action":"fail","Package":"example.com/pkg"}
`
			return runner.MockProcess([]byte(output), fmt.Errorf("exit status 1")), nil
		}
		if proc, ok := handleGoList(cfg); ok {
			return proc, nil
		}
		return nil, nil
	}
	return mock
}

func TestRunWithRunnerModTidyFails(t *testing.T) {
	mock := newModTidyFailMock()

	jsonOutput = true
	defer func() { jsonOutput = false }()

	err := runWithRunner(mock)
	assert.NotNil(t, err)
}

func TestRunWithRunnerTestsFail(t *testing.T) {
	mock := newTestPipesFailMock()

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

	mock := newTestPassMock(50)

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

	mock := newTestPassMock(0)

	jsonOutput = true
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		outputDir = "build"
	}()

	err := runWithRunner(mock)
	assert.Nil(t, err)
}

func TestRunWithRunnerCGODisabledByDefault(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	oldCgo := cgoEnabled
	cgoEnabled = false
	defer func() { cgoEnabled = oldCgo }()

	mock := newTestPassMock(0)

	jsonOutput = true
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		outputDir = "build"
	}()

	err := runWithRunner(mock)
	assert.Nil(t, err)

	// Verify CGO_ENABLED=0 was set on the build command
	for _, cfg := range mock.Calls() {
		if cfg.IsCmd("go", "build") {
			assert.Equal(t, "0", cfg.Env["CGO_ENABLED"], "CGO should be disabled by default")
		}
	}
}

func TestRunWithRunnerCGOEnabledFlag(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	oldCgo := cgoEnabled
	cgoEnabled = true
	defer func() { cgoEnabled = oldCgo }()

	mock := newTestPassMock(0)

	jsonOutput = true
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		outputDir = "build"
	}()

	err := runWithRunner(mock)
	assert.Nil(t, err)

	// Verify CGO_ENABLED was NOT set on the build command
	for _, cfg := range mock.Calls() {
		if cfg.IsCmd("go", "build") {
			_, hasCgo := cfg.Env["CGO_ENABLED"]
			assert.False(t, hasCgo, "CGO_ENABLED should not be set when --cgo is used")
		}
	}
}

func TestRunWithRunnerSuccessVerbose(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := newTestPassMock(0)

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

	mock := newTestPassMock(0)

	jsonOutput = false
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		outputDir = "build"
	}()

	err := runWithRunner(mock)
	assert.Nil(t, err)
}

func TestRunWithRunnerBuildFails(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := newBuildFailMock()

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

	mock := newTestPassMock(50)

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

	mock := newTestPassMock(50)

	jsonOutput = true // JSON output path when below threshold
	defer func() { jsonOutput = false }()

	err := runWithRunner(mock)
	assert.NotNil(t, err)
}

func TestIgnoreCoverage(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	err := runIgnoreCoverage(nil, nil)
	assert.Nil(t, err)

	wm, exists, werr := gotest.GetWatermark(".")
	require.Nil(t, werr)
	require.True(t, exists)
	assert.Equal(t, float32(0), wm)
}

func TestIgnoreCoverageAlreadyExists(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	gotest.SetWatermark(".", 85.0)

	// Should not error, just report existing
	err := runIgnoreCoverage(nil, nil)
	assert.Nil(t, err)

	// Verify watermark was NOT changed
	wm, exists, _ := gotest.GetWatermark(".")
	assert.True(t, exists)
	assert.Equal(t, float32(85.0), wm)
}

func TestUnignoreCoverage(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	gotest.SetWatermark(".", 90.0)

	// runUnignoreCoverage is called after PersistentPreRunE confirmation
	err := runUnignoreCoverage(nil, nil)
	assert.Nil(t, err)

	_, exists, _ := gotest.GetWatermark(".")
	assert.False(t, exists)
}

func TestUnignoreCoverageNoWatermark(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Should not error when no watermark exists
	err := runUnignoreCoverage(nil, nil)
	assert.Nil(t, err)
}

func TestUnignoreConfirmationAbort(t *testing.T) {
	// Simulate "n" input to PersistentPreRunE
	oldStdin := os.Stdin
	rIn, wIn, _ := os.Pipe()
	wIn.WriteString("n\n")
	wIn.Close()
	os.Stdin = rIn
	defer func() { os.Stdin = oldStdin }()

	err := unignoreCmd.PersistentPreRunE(nil, nil)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "aborted")
}

func TestUnignoreConfirmationAccept(t *testing.T) {
	// Simulate "y" input to PersistentPreRunE
	oldStdin := os.Stdin
	rIn, wIn, _ := os.Pipe()
	wIn.WriteString("y\n")
	wIn.Close()
	os.Stdin = rIn
	defer func() { os.Stdin = oldStdin }()

	err := unignoreCmd.PersistentPreRunE(nil, nil)
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

	mock := newTestPassMock(50)

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

	mock := newTestPassMock(50)

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

	mock := newTestPassMock(0)

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
	assert.Equal(t, float32(100.0), wm)
}

func TestUnignoreCoverageNoWatermarkMessage(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runUnignoreCoverage(nil, nil)

	w.Close()
	out, _ := io.ReadAll(r)
	os.Stdout = old

	assert.Nil(t, err)
	assert.Contains(t, string(out), "No watermark is set")
}


func TestRunWithRunnerFailedTest(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := newTestFailMock()

	jsonOutput = false
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		outputDir = "build"
	}()

	err := runWithRunner(mock)
	assert.Nil(t, err)
}

func TestRunWithRunnerTestsFailWithOutput(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := newTestFailWithErrorMock()

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

func TestFindGoModules_CurrentDir(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\ngo 1.21\n"), 0644)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	modules := findGoModules()
	require.Equal(t, 1, len(modules))
	assert.Equal(t, ".", modules[0])
}

func TestFindGoModules_Subdirectories(t *testing.T) {
	dir := t.TempDir()
	// No go.mod in root — create subdirectories with go.mod
	os.MkdirAll(filepath.Join(dir, "svc-a"), 0755)
	os.MkdirAll(filepath.Join(dir, "svc-b"), 0755)
	os.WriteFile(filepath.Join(dir, "svc-a", "go.mod"), []byte("module test/a\ngo 1.21\n"), 0644)
	os.WriteFile(filepath.Join(dir, "svc-b", "go.mod"), []byte("module test/b\ngo 1.21\n"), 0644)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	modules := findGoModules()
	assert.Equal(t, 2, len(modules))
}

func TestFindGoModules_SkipsHiddenAndVendor(t *testing.T) {
	dir := t.TempDir()
	// No go.mod in root
	os.MkdirAll(filepath.Join(dir, ".hidden"), 0755)
	os.MkdirAll(filepath.Join(dir, "vendor"), 0755)
	os.MkdirAll(filepath.Join(dir, "node_modules"), 0755)
	os.MkdirAll(filepath.Join(dir, "real"), 0755)
	os.WriteFile(filepath.Join(dir, ".hidden", "go.mod"), []byte("module hidden\n"), 0644)
	os.WriteFile(filepath.Join(dir, "vendor", "go.mod"), []byte("module vendor\n"), 0644)
	os.WriteFile(filepath.Join(dir, "node_modules", "go.mod"), []byte("module nm\n"), 0644)
	os.WriteFile(filepath.Join(dir, "real", "go.mod"), []byte("module real\n"), 0644)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	modules := findGoModules()
	require.Equal(t, 1, len(modules))
	assert.Equal(t, "real", modules[0])
}

func TestFindGoModules_NoModules(t *testing.T) {
	dir := t.TempDir()

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	modules := findGoModules()
	assert.Equal(t, 0, len(modules))
}
