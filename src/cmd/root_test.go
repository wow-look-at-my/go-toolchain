package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	gotest "github.com/wow-look-at-my/go-toolchain/src/test"
)

func TestRunWithRunnerModTidyFails(t *testing.T) {
	mock := newModTidyFailMock()
	jsonOutput = true
	defer func() { jsonOutput = false }()
	err := runWithRunner(mock, nil)
	assert.NotNil(t, err)
}

func TestRunWithRunnerTestsFail(t *testing.T) {
	mock := newTestPipesFailMock()
	jsonOutput = true
	defer func() { jsonOutput = false }()
	err := runWithRunner(mock, nil)
	assert.NotNil(t, err)
}

func TestRunWithRunnerCoverageBelowThreshold(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject(t)
	mock := newTestPassMock(50)
	jsonOutput = true
	defer func() { jsonOutput = false }()
	err := runWithRunner(mock, nil)
	assert.NotNil(t, err)
}

func TestRunWithRunnerSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject(t)
	mock := newTestPassMock(0)
	jsonOutput = true
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		outputDir = "build"
	}()
	err := runWithRunner(mock, nil)
	assert.Nil(t, err)
}

// The default build writes the plain name, everywhere. A platform suffix
// would claim a property the fat APE does not have -- it runs on every host --
// and there is no host-shaped build left for it to distinguish.
func TestRunWithRunnerBinaryNameCarriesNoPlatform(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject(t)

	mock := newTestPassMock(0)
	jsonOutput = true
	outputDir = tmpDir
	defer func() { jsonOutput = false; outputDir = "build" }()

	require.NoError(t, runWithRunner(mock, nil))

	suffix := fmt.Sprintf("_%s_%s", runtime.GOOS, runtime.GOARCH)
	sawBuild := false
	for _, cfg := range mock.Calls() {
		if !isGoBuild(cfg) {
			continue
		}
		for i, arg := range cfg.Args {
			if arg == "-o" && i+1 < len(cfg.Args) {
				sawBuild = true
				assert.NotContains(t, filepath.Base(cfg.Args[i+1]), suffix)
			}
		}
	}
	assert.True(t, sawBuild, "no go build -o call was made, so the assertion above proved nothing")
}

func TestRunWithRunnerCGODisabledByDefault(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject(t)

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

	err := runWithRunner(mock, nil)
	assert.Nil(t, err)

	// Verify CGO_ENABLED=0 was set on the build command
	for _, cfg := range mock.Calls() {
		if isGoBuild(cfg) {
			cgo, _ := cfg.Env.Get("CGO_ENABLED")
			assert.Equal(t, "0", cgo, "CGO should be disabled by default")
		}
	}
}

func TestRunWithRunnerCGOEnabledFlag(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject(t)

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

	err := runWithRunner(mock, nil)
	assert.Nil(t, err)

	// --cgo cannot reach the build: the APE has no cgo, so CGO_ENABLED stays 0.
	for _, cfg := range mock.Calls() {
		if isGoBuild(cfg) {
			cgo, _ := cfg.Env.Get("CGO_ENABLED")
			assert.Equal(t, "0", cgo, "--cgo must not turn cgo on for the APE")
		}
	}
}

func TestRunWithRunnerSuccessVerbose(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject(t)

	mock := newTestPassMock(0)

	jsonOutput = false
	verbose = true
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		verbose = false
		outputDir = "build"
	}()

	err := runWithRunner(mock, nil)
	assert.Nil(t, err)
}

func TestRunWithRunnerNonJSON(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject(t)

	mock := newTestPassMock(0)

	jsonOutput = false
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		outputDir = "build"
	}()

	err := runWithRunner(mock, nil)
	assert.Nil(t, err)
}

func TestRunWithRunnerBuildFails(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject(t)

	mock := newBuildFailMock()

	jsonOutput = true
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		outputDir = "build"
	}()

	err := runWithRunner(mock, nil)
	assert.NotNil(t, err)
}

func TestRunWithRunnerCoverageBelowThresholdNonJSON(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject(t)

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
	setupMockProject(t)

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
	setupMockProject(t)
	// Set watermark to 60% — grace = 57.5, effective = min(80, 57.5) = 57.5
	gotest.SetWatermark(".", 60.0)
	mock := newTestPassMock(50)
	jsonOutput = false
	defer func() { jsonOutput = false }()
	err := runWithRunner(mock, nil)
	assert.NotNil(t, err)
	assert.False(t, err != nil && !strings.Contains(err.Error(), "below minimum"))
}

func TestRunWithRunnerBrokenCoverageDataPanics(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject(t)
	// Coverable statements below are what make the empty profile "broken".
	os.WriteFile(filepath.Join("pkg", "main.go"), []byte("package main\n\nfunc main() { println(\"x\") }\n"), 0644)

	// A passing test that writes an empty ("mode: set" only) coverage profile.
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
			setupMockProject(t)
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
	setupMockProject(t)

	// Watermark 52% -> grace 49.5, effective min(80, 49.5) = 49.5; 50 passes.
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
	setupMockProject(t)

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
	setupMockProject(t)

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
	setupMockProject(t)

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
