package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// With no target flags the run takes the single-APE path, which resolves the
// gosmopolitan toolchain rather than building a per-platform product.
func TestRunReleaseWithRunnerNoPlatformsBuildsTheAPE(t *testing.T) {
	oldOS, oldArch, oldTargets := matrixOS, matrixArch, matrixTargets
	oldEnsure := ensureCosmoToolchainFunc
	matrixOS, matrixArch, matrixTargets = nil, nil, nil
	ensureCosmoToolchainFunc = func() (string, error) {
		return "", fmt.Errorf("cosmo toolchain unavailable")
	}
	defer func() {
		matrixOS, matrixArch, matrixTargets = oldOS, oldArch, oldTargets
		ensureCosmoToolchainFunc = oldEnsure
	}()

	mock := runner.NewMock()
	err := runReleaseWithRunner(mock, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cosmo toolchain unavailable")
}

func TestRunReleaseWithRunnerNoMainPackages(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	oldOS := matrixOS
	oldArch := matrixArch
	oldOutput := outputDir
	matrixOS = []string{"linux"}
	matrixArch = []string{"amd64"}
	outputDir = filepath.Join(tmpDir, "dist")
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
		outputDir = oldOutput
	}()

	mock := runner.NewMock()
	err := runReleaseWithRunner(mock, nil)
	assert.NotNil(t, err)
}

func TestRunReleaseWithRunnerSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Create a minimal go file
	os.WriteFile("main.go", []byte("package main\nfunc main() {}\n"), 0644)

	oldOS := matrixOS
	oldArch := matrixArch
	oldOutput := outputDir
	oldParallel := releaseParallel
	matrixOS = []string{"linux"}
	matrixArch = []string{"amd64", "arm64"}
	outputDir = filepath.Join(tmpDir, "dist")
	releaseParallel = 2
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
		outputDir = oldOutput
		releaseParallel = oldParallel
	}()

	mock := newTestPassMock(0)
	err := runReleaseWithRunner(mock, nil)
	assert.Nil(t, err)
}

func TestRunReleaseWithRunnerBuildFails(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Create a minimal go file
	os.WriteFile("main.go", []byte("package main\nfunc main() {}\n"), 0644)

	oldOS := matrixOS
	oldArch := matrixArch
	oldOutput := outputDir
	oldParallel := releaseParallel
	matrixOS = []string{"linux"}
	matrixArch = []string{"amd64"}
	outputDir = filepath.Join(tmpDir, "dist")
	releaseParallel = 1
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
		outputDir = oldOutput
		releaseParallel = oldParallel
	}()

	// Use a mock that passes tests but fails builds
	mock := newTestPassMock(0)
	origHandler := mock.Handler
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "build") {
			return nil, fmt.Errorf("build failed")
		}
		return origHandler(cfg)
	}
	err := runReleaseWithRunner(mock, nil)
	assert.NotNil(t, err)
}

func TestRunReleaseWithRunnerWindowsExt(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Create a minimal go file
	os.WriteFile("main.go", []byte("package main\nfunc main() {}\n"), 0644)

	oldOS := matrixOS
	oldArch := matrixArch
	oldOutput := outputDir
	oldParallel := releaseParallel
	matrixOS = []string{"windows"}
	matrixArch = []string{"amd64"}
	outputDir = filepath.Join(tmpDir, "dist")
	releaseParallel = 1
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
		outputDir = oldOutput
		releaseParallel = oldParallel
	}()

	mock := newTestPassMock(0)
	err := runReleaseWithRunner(mock, nil)
	assert.Nil(t, err)

	// Check that commands were recorded with .exe extension
	found := false
	for _, cfg := range mock.Calls() {
		if cfg.IsCmd("go", "build") {
			for i, arg := range cfg.Args {
				if arg == "-o" && i+1 < len(cfg.Args) {
					if filepath.Ext(cfg.Args[i+1]) == ".exe" {
						found = true
					}
				}
			}
		}
	}
	assert.True(t, found)
}

func TestRunReleaseWithRunnerMoreJobsThanWorkers(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Create a minimal go file
	os.WriteFile("main.go", []byte("package main\nfunc main() {}\n"), 0644)

	oldOS := matrixOS
	oldArch := matrixArch
	oldOutput := outputDir
	oldParallel := releaseParallel
	matrixOS = []string{"linux"}
	matrixArch = []string{"amd64"}
	outputDir = filepath.Join(tmpDir, "dist")
	releaseParallel = 10 // More workers than jobs
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
		outputDir = oldOutput
		releaseParallel = oldParallel
	}()

	mock := newTestPassMock(0)
	err := runReleaseWithRunner(mock, nil)
	assert.Nil(t, err)
}

func TestRunReleaseWithRunnerMultipleOSArch(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Create a minimal go file
	os.WriteFile("main.go", []byte("package main\nfunc main() {}\n"), 0644)

	oldOS := matrixOS
	oldArch := matrixArch
	oldOutput := outputDir
	oldParallel := releaseParallel
	matrixOS = []string{"linux", "darwin"}
	matrixArch = []string{"amd64", "arm64"}
	outputDir = filepath.Join(tmpDir, "dist")
	releaseParallel = 4
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
		outputDir = oldOutput
		releaseParallel = oldParallel
	}()

	mock := newTestPassMock(0)
	err := runReleaseWithRunner(mock, nil)
	assert.Nil(t, err)

	// Should have 4 builds: 2 OS x 2 arch
	buildCount := 0
	for _, cfg := range mock.Calls() {
		if cfg.IsCmd("go", "build") {
			buildCount++
		}
	}
	assert.Equal(t, 4, buildCount)
}

func TestRunReleaseWithRunnerRunsBenchmarks(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("main.go", []byte("package main\nfunc main() {}\n"), 0644)
	os.WriteFile("x_test.go", []byte("package main\nimport \"testing\"\nfunc BenchmarkX(b *testing.B) {}\n"), 0644)

	oldOS := matrixOS
	oldArch := matrixArch
	oldOutput := outputDir
	oldParallel := releaseParallel
	oldBench := noBenchmark
	oldJSON := jsonOutput
	matrixOS = []string{"linux"}
	matrixArch = []string{"amd64"}
	outputDir = filepath.Join(tmpDir, "dist")
	releaseParallel = 1
	noBenchmark = false
	jsonOutput = true
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
		outputDir = oldOutput
		releaseParallel = oldParallel
		noBenchmark = oldBench
		jsonOutput = oldJSON
	}()

	mock := newTestPassMock(0)
	err := runReleaseWithRunner(mock, nil)
	assert.Nil(t, err)

	// Verify that a benchmark command was issued
	found := false
	for _, cfg := range mock.Calls() {
		if cfg.IsCmd("go", "test") && cfg.HasArg("-bench") {
			found = true
			break
		}
	}
	assert.True(t, found, "matrix command should run benchmarks by default")
}

func TestRunReleaseWithRunnerNoBenchmarkFlag(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("main.go", []byte("package main\nfunc main() {}\n"), 0644)

	oldOS := matrixOS
	oldArch := matrixArch
	oldOutput := outputDir
	oldParallel := releaseParallel
	oldBench := noBenchmark
	matrixOS = []string{"linux"}
	matrixArch = []string{"amd64"}
	outputDir = filepath.Join(tmpDir, "dist")
	releaseParallel = 1
	noBenchmark = true
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
		outputDir = oldOutput
		releaseParallel = oldParallel
		noBenchmark = oldBench
	}()

	mock := newTestPassMock(0)
	err := runReleaseWithRunner(mock, nil)
	assert.Nil(t, err)

	// Verify no benchmark command was issued
	for _, cfg := range mock.Calls() {
		if cfg.IsCmd("go", "test") {
			assert.False(t, cfg.HasArg("-bench"), "should not have -bench flag when --no-benchmark is set")
		}
	}
}

func TestMatrixOutputShowsProgressAndDuration(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("main.go", []byte("package main\nfunc main() {}\n"), 0644)

	oldOS := matrixOS
	oldArch := matrixArch
	oldOutput := outputDir
	oldParallel := releaseParallel
	oldBench := noBenchmark
	matrixOS = []string{"linux"}
	matrixArch = []string{"amd64", "arm64"}
	outputDir = filepath.Join(tmpDir, "dist")
	releaseParallel = 1
	noBenchmark = true
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
		outputDir = oldOutput
		releaseParallel = oldParallel
		noBenchmark = oldBench
	}()

	mock := newTestPassMock(0)
	output := captureStdout(func() {
		err := runReleaseWithRunner(mock, nil)
		assert.Nil(t, err)
	})

	// Each OK line should show [N/2] counter and duration in seconds (no parentheses)
	okPattern := regexp.MustCompile(`OK\s+\[(\d+)/2\].*\d+\.\d+s`)
	okMatches := okPattern.FindAllString(output, -1)
	assert.Equal(t, 2, len(okMatches), "expected 2 OK lines with progress counters and durations, got: %v", okMatches)

	// Summary line should show total duration
	assert.Regexp(t, `All 2 binaries built successfully.*\d+\.\d+s`, output)
}

func TestMatrixOutputFailureShowsDuration(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("main.go", []byte("package main\nfunc main() {}\n"), 0644)

	oldOS := matrixOS
	oldArch := matrixArch
	oldOutput := outputDir
	oldParallel := releaseParallel
	oldBench := noBenchmark
	matrixOS = []string{"linux"}
	matrixArch = []string{"amd64"}
	outputDir = filepath.Join(tmpDir, "dist")
	releaseParallel = 1
	noBenchmark = true
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
		outputDir = oldOutput
		releaseParallel = oldParallel
		noBenchmark = oldBench
	}()

	mock := newTestPassMock(0)
	origHandler := mock.Handler
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "build") {
			return nil, fmt.Errorf("build failed")
		}
		return origHandler(cfg)
	}

	output := captureStdout(func() {
		err := runReleaseWithRunner(mock, nil)
		assert.NotNil(t, err)
	})

	// FAIL line should show [1/1] counter and duration (no parentheses)
	assert.Regexp(t, `FAIL \[1/1\].*\d+\.\d+s`, output)
}
