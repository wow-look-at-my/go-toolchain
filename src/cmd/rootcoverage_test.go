package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	gotest "github.com/wow-look-at-my/go-toolchain/src/test"
)

func TestRunWithRunnerCoverageBelowThresholdNonJSON(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject()

	mock := newTestPassMock(50)

	jsonOutput = false // Non-JSON output to hit uncovered functions display
	defer func() { jsonOutput = false }()

	err := runWithRunner(mock, nil)
	assert.NotNil(t, err)
}

func TestRunWithRunnerCoverageBelowThresholdJSON(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject()

	mock := newTestPassMock(50)

	jsonOutput = true // JSON output path when below threshold
	defer func() { jsonOutput = false }()

	err := runWithRunner(mock, nil)
	assert.NotNil(t, err)
}

func TestRunWithRunnerWatermarkEnforcement(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject()
	// Set watermark to 60% — grace = 57.5, effective = min(80, 57.5) = 57.5
	gotest.SetWatermark(".", 60.0)
	mock := newTestPassMock(50)
	jsonOutput = false
	defer func() { jsonOutput = false }()
	err := runWithRunner(mock, nil)
	assert.NotNil(t, err)
	assert.False(t, err != nil && !strings.Contains(err.Error(), "below minimum"))
}

func newSmallMock(covered, uncovered int) *runner.Mock {
	pct := float32(covered) / float32(covered+uncovered) * 100
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "test") {
			writeMockCoverProfileStmts(cfg.Args, covered, uncovered)
			out := fmt.Sprintf("{\"Time\":\"2024-01-01T00:00:00Z\",\"Action\":\"run\",\"Package\":\"example.com/pkg\"}\n{\"Time\":\"2024-01-01T00:00:01Z\",\"Action\":\"output\",\"Package\":\"example.com/pkg\",\"Output\":\"coverage: %.1f%% of statements\\n\"}\n{\"Time\":\"2024-01-01T00:00:02Z\",\"Action\":\"pass\",\"Package\":\"example.com/pkg\"}\n", pct)
			return runner.MockProcess([]byte(out), nil), nil
		}
		if proc, ok := handleGoList(cfg); ok {
			return proc, nil
		}
		return nil, nil
	}
	return mock
}

func TestRunWithRunnerBrokenCoverageDataPanics(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject()
	// Coverable statements + test results below are what make the empty
	// profile "broken" rather than a legitimately zero-statement module.
	os.WriteFile(filepath.Join("pkg", "main.go"), []byte("package main\n\nfunc main() { println(\"x\") }\n"), 0644)

	// Mock that reports a passing test but writes an empty coverage profile
	// (just "mode: set"). This simulates broken coverage data collection.
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "test") {
			writeMockCoverProfileStmts(cfg.Args, 0, 0) // empty profile
			out := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg","Test":"TestX"}` + "\n" +
				`{"Time":"2024-01-01T00:00:01Z","Action":"pass","Package":"example.com/pkg","Test":"TestX","Elapsed":0.01}` + "\n" +
				`{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg"}` + "\n"
			return runner.MockProcess([]byte(out), nil), nil
		}
		if proc, ok := handleGoList(cfg); ok {
			return proc, nil
		}
		return nil, nil
	}

	jsonOutput = true
	defer func() { jsonOutput = false }()
	assert.Panics(t, func() {
		runWithRunner(mock, nil) //nolint
	})
}

func TestRunWithRunnerReducedCoverageSmallProgram(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cov, unc int
		wantErr  bool
	}{
		{"5 uncovered allows", 12, 5, false}, // 70.6% < 80% but 5 < 10
		{"40 uncovered fails", 60, 40, true}, // 60% < 80% and 40 >= 10
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			oldWd, _ := os.Getwd()
			os.Chdir(tmpDir)
			defer os.Chdir(oldWd)
			setupMockProject()
			jsonOutput = false
			err := runWithRunner(newSmallMock(tc.cov, tc.unc), nil)
			if tc.wantErr {
				assert.NotNil(t, err)
				assert.Contains(t, err.Error(), "below minimum")
			} else {
				assert.Nil(t, err)
			}
		})
	}
}

func TestRunWithRunnerWatermarkGracePass(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject()

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

	err := runWithRunner(mock, nil)
	assert.Nil(t, err)
}

func TestRunWithRunnerWatermarkRatchetUp(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject()

	// Set watermark to 50% — coverage is 100%, should ratchet up
	gotest.SetWatermark(".", 50.0)

	mock := newTestPassMock(0)

	jsonOutput = false
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		outputDir = "build"
	}()

	err := runWithRunner(mock, nil)
	assert.Nil(t, err)

	// Verify watermark was ratcheted up
	wm, _, _ := gotest.GetWatermark(".")
	assert.Equal(t, float32(100.0), wm)
}

func TestRunWithRunnerFailedTest(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject()

	mock := newTestFailMock()

	jsonOutput = false
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		outputDir = "build"
	}()

	err := runWithRunner(mock, nil)
	assert.Nil(t, err)
}

func TestRunWithRunnerTestsFailWithOutput(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject()

	mock := newTestFailWithErrorMock()

	jsonOutput = false
	verbose = true // Should show test output before error
	defer func() {
		jsonOutput = false
		verbose = false
	}()

	err := runWithRunner(mock, nil)
	assert.NotNil(t, err)
	// The key point: results are still displayed before the error is returned
}
