package cmd

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"github.com/wow-look-at-my/go-toolchain/src/summary"
)

// With no target flags the run takes the single-APE path, which needs the go
// command this binary is rather than building a per-platform product.
func TestRunReleaseWithRunnerNoPlatformsBuildsTheAPE(t *testing.T) {
	t.Serial()
	oldTargets, oldCmd := matrixTargets, activeGoCmd
	matrixTargets = nil
	activeGoCmd = nil
	defer func() {
		matrixTargets, activeGoCmd = oldTargets, oldCmd
	}()

	mock := runner.NewMock()
	err := runReleaseWithRunner(mock, nil)
	require.Error(t, err)
<<<<<<< HEAD
<<<<<<< HEAD
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
=======
	assert.Contains(t, err.Error(), "no go command")
>>>>>>> origin/master
=======
	assert.Contains(t, err.Error(), "no go command")
>>>>>>> origin/master
}

func TestRunReleaseWithRunnerSuccess(t *testing.T) {
	t.Serial()
	fakeGoroot, _ := setupCosmoMatrixTest(t, []string{"wasm/js", "wasm/wasip1"})
	releaseParallel = 2

	mock := newTestPassMock(0)
<<<<<<< HEAD
<<<<<<< HEAD
	err := runReleaseWithRunner(mock, nil)
=======
=======
	origHandler := mock.Handler
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if isForkBuild(cfg, fakeGoroot) {
			writeBuildOutput(t, cfg, "WASM")
			return runner.MockProcess(nil, nil), nil
		}
		return origHandler(cfg)
	}
	err := runReleaseWithRunner(mock)
	assert.Nil(t, err)
}

// The step summary a matrix run writes carries the coverage its test phase measured.
func TestRunReleaseIntoRecordsTheTestPhase(t *testing.T) {
	t.Serial()
	fakeGoroot, _ := setupCosmoMatrixTest(t, []string{"wasm/js"})
	releaseParallel = 1

	mock := newTestPassMock(0)
>>>>>>> origin/master
	origHandler := mock.Handler
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if isForkBuild(cfg, fakeGoroot) {
			writeBuildOutput(t, cfg, "WASM")
			return runner.MockProcess(nil, nil), nil
<<<<<<< HEAD
=======
		}
		return origHandler(cfg)
	}
	var sd summary.SummaryData
	require.NoError(t, runReleaseInto(mock, &sd))
	assert.NotNil(t, sd.Coverage, "the summary must carry the test phase's coverage")
}

func TestRunReleaseWithRunnerBuildFails(t *testing.T) {
	t.Serial()
	fakeGoroot, _ := setupCosmoMatrixTest(t, []string{"wasm/js"})
	releaseParallel = 1

	// Use a mock that passes tests but fails builds.
	mock := newTestPassMock(0)
	origHandler := mock.Handler
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if isForkBuild(cfg, fakeGoroot) {
			return nil, fmt.Errorf("build failed")
>>>>>>> origin/master
		}
		return origHandler(cfg)
	}
	err := runReleaseWithRunner(mock)
>>>>>>> origin/master
	assert.Nil(t, err)
}

func TestRunReleaseWithRunnerBuildFails(t *testing.T) {
	t.Serial()
	fakeGoroot, _ := setupCosmoMatrixTest(t, []string{"wasm/js"})
	releaseParallel = 1

	// Use a mock that passes tests but fails builds.
	mock := newTestPassMock(0)
	origHandler := mock.Handler
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if isForkBuild(cfg, fakeGoroot) {
			return nil, fmt.Errorf("build failed")
		}
		return origHandler(cfg)
	}
	err := runReleaseWithRunner(mock, nil)
	assert.NotNil(t, err)
}

<<<<<<< HEAD
<<<<<<< HEAD
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

=======
>>>>>>> origin/master
=======
>>>>>>> origin/master
func TestRunReleaseWithRunnerMoreJobsThanWorkers(t *testing.T) {
	t.Serial()
	fakeGoroot, _ := setupCosmoMatrixTest(t, []string{"wasm/js", "wasm/wasip1"})
	releaseParallel = 10 // More workers than jobs

	mock := newTestPassMock(0)
<<<<<<< HEAD
<<<<<<< HEAD
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
=======
=======
>>>>>>> origin/master
	origHandler := mock.Handler
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if isForkBuild(cfg, fakeGoroot) {
			writeBuildOutput(t, cfg, "WASM")
			return runner.MockProcess(nil, nil), nil
<<<<<<< HEAD
>>>>>>> origin/master
=======
>>>>>>> origin/master
		}
		return origHandler(cfg)
	}
	err := runReleaseWithRunner(mock)
	assert.Nil(t, err)
}

func TestRunReleaseWithRunnerRunsBenchmarks(t *testing.T) {
	t.Serial()
	fakeGoroot, _ := setupCosmoMatrixTest(t, []string{"wasm/js"})
	// Canonical spacing, like main.go: the module is real, so in CI vet checks this fixture instead of rewriting it.
	os.WriteFile("x_test.go", []byte("package main\n\nimport \"testing\"\n\nfunc BenchmarkX(b *testing.B) {}\n"), 0644)

	oldJSON := jsonOutput
	releaseParallel = 1
	noBenchmark = false
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	mock := newTestPassMock(0)
<<<<<<< HEAD
<<<<<<< HEAD
	err := runReleaseWithRunner(mock, nil)
=======
=======
>>>>>>> origin/master
	origHandler := mock.Handler
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if isForkBuild(cfg, fakeGoroot) {
			writeBuildOutput(t, cfg, "WASM")
			return runner.MockProcess(nil, nil), nil
		}
		return origHandler(cfg)
	}
	err := runReleaseWithRunner(mock)
>>>>>>> origin/master
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
	t.Serial()
	fakeGoroot, _ := setupCosmoMatrixTest(t, []string{"wasm/js"})
	releaseParallel = 1
	noBenchmark = true

	mock := newTestPassMock(0)
<<<<<<< HEAD
<<<<<<< HEAD
	err := runReleaseWithRunner(mock, nil)
=======
=======
>>>>>>> origin/master
	origHandler := mock.Handler
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if isForkBuild(cfg, fakeGoroot) {
			writeBuildOutput(t, cfg, "WASM")
			return runner.MockProcess(nil, nil), nil
		}
		return origHandler(cfg)
	}
	err := runReleaseWithRunner(mock)
>>>>>>> origin/master
	assert.Nil(t, err)

	// Verify no benchmark command was issued
	for _, cfg := range mock.Calls() {
		if cfg.IsCmd("go", "test") {
			assert.False(t, cfg.HasArg("-bench"), "should not have -bench flag when --no-benchmark is set")
		}
	}
}

func TestMatrixOutputShowsProgressAndDuration(t *testing.T) {
	t.Serial()
	fakeGoroot, _ := setupCosmoMatrixTest(t, []string{"wasm/js", "wasm/wasip1"})
	releaseParallel = 1

	mock := newTestPassMock(0)
	origHandler := mock.Handler
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if isForkBuild(cfg, fakeGoroot) {
			writeBuildOutput(t, cfg, "WASM")
			return runner.MockProcess(nil, nil), nil
		}
		return origHandler(cfg)
	}
	output := captureStdout(func() {
		err := runReleaseWithRunner(mock, nil)
		assert.Nil(t, err)
	})

	// Each OK line should show its progress counter and a duration (no parentheses)
	okPattern := regexp.MustCompile(`OK\s+\[(\d+)/2\].*\d+\.\d+s`)
	okMatches := okPattern.FindAllString(output, -1)
	assert.Equal(t, 2, len(okMatches), "expected 2 OK lines with progress counters and durations, got: %v", okMatches)

	// Summary line should show total duration
	assert.Regexp(t, `All 2 binaries built successfully.*\d+\.\d+s`, output)
}

func TestMatrixOutputFailureShowsDuration(t *testing.T) {
	t.Serial()
	fakeGoroot, _ := setupCosmoMatrixTest(t, []string{"wasm/js"})
	releaseParallel = 1

	mock := newTestPassMock(0)
	origHandler := mock.Handler
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if isForkBuild(cfg, fakeGoroot) {
			return nil, fmt.Errorf("build failed")
		}
		return origHandler(cfg)
	}

	output := captureStdout(func() {
		err := runReleaseWithRunner(mock, nil)
		assert.NotNil(t, err)
	})

	// The FAIL line should show its progress counter and a duration (no parentheses)
	assert.Regexp(t, `FAIL \[1/1\].*\d+\.\d+s`, output)
}
