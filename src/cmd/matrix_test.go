package cmd

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// With no target flags the run takes the single-APE path, which resolves the
// gosmopolitan toolchain rather than building a per-platform product.
func TestRunReleaseWithRunnerNoPlatformsBuildsTheAPE(t *testing.T) {
	oldTargets := matrixTargets
	oldEnsure := ensureCosmoToolchainFunc
	matrixTargets = nil
	ensureCosmoToolchainFunc = func() (string, error) {
		return "", fmt.Errorf("cosmo toolchain unavailable")
	}
	defer func() {
		matrixTargets = oldTargets
		ensureCosmoToolchainFunc = oldEnsure
	}()

	mock := runner.NewMock()
	err := runReleaseWithRunner(mock)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cosmo toolchain unavailable")
}

func TestRunReleaseWithRunnerSuccess(t *testing.T) {
	fakeGoroot, _ := setupCosmoMatrixTest(t, []string{"wasm/js", "wasm/wasip1"})
	releaseParallel = 2

	mock := newTestPassMock(0)
	origHandler := mock.Handler
	// The production spelling; NT adds .exe. Hand-spelled, it never matches.
	forkGo := cosmoGoBinPath(fakeGoroot)
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.Name == forkGo && len(cfg.Args) > 0 && cfg.Args[0] == "build" {
			writeBuildOutput(t, cfg, "WASM")
			return runner.MockProcess(nil, nil), nil
		}
		return origHandler(cfg)
	}
	err := runReleaseWithRunner(mock)
	assert.Nil(t, err)
}

func TestRunReleaseWithRunnerBuildFails(t *testing.T) {
	fakeGoroot, _ := setupCosmoMatrixTest(t, []string{"wasm/js"})
	releaseParallel = 1

	// Use a mock that passes tests but fails builds.
	mock := newTestPassMock(0)
	origHandler := mock.Handler
	forkGo := cosmoGoBinPath(fakeGoroot)
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.Name == forkGo && len(cfg.Args) > 0 && cfg.Args[0] == "build" {
			return nil, fmt.Errorf("build failed")
		}
		return origHandler(cfg)
	}
	err := runReleaseWithRunner(mock)
	assert.NotNil(t, err)
}

func TestRunReleaseWithRunnerMoreJobsThanWorkers(t *testing.T) {
	fakeGoroot, _ := setupCosmoMatrixTest(t, []string{"wasm/js", "wasm/wasip1"})
	releaseParallel = 10 // More workers than jobs

	mock := newTestPassMock(0)
	origHandler := mock.Handler
	forkGo := cosmoGoBinPath(fakeGoroot)
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.Name == forkGo && len(cfg.Args) > 0 && cfg.Args[0] == "build" {
			writeBuildOutput(t, cfg, "WASM")
			return runner.MockProcess(nil, nil), nil
		}
		return origHandler(cfg)
	}
	err := runReleaseWithRunner(mock)
	assert.Nil(t, err)
}

func TestRunReleaseWithRunnerRunsBenchmarks(t *testing.T) {
	fakeGoroot, _ := setupCosmoMatrixTest(t, []string{"wasm/js"})
	// Canonical spacing, like main.go: the module is real, so in CI vet checks this fixture instead of rewriting it.
	os.WriteFile("x_test.go", []byte("package main\n\nimport \"testing\"\n\nfunc BenchmarkX(b *testing.B) {}\n"), 0644)

	oldJSON := jsonOutput
	releaseParallel = 1
	noBenchmark = false
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	mock := newTestPassMock(0)
	origHandler := mock.Handler
	forkGo := cosmoGoBinPath(fakeGoroot)
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.Name == forkGo && len(cfg.Args) > 0 && cfg.Args[0] == "build" {
			writeBuildOutput(t, cfg, "WASM")
			return runner.MockProcess(nil, nil), nil
		}
		return origHandler(cfg)
	}
	err := runReleaseWithRunner(mock)
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
	fakeGoroot, _ := setupCosmoMatrixTest(t, []string{"wasm/js"})
	releaseParallel = 1
	noBenchmark = true

	mock := newTestPassMock(0)
	origHandler := mock.Handler
	forkGo := cosmoGoBinPath(fakeGoroot)
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.Name == forkGo && len(cfg.Args) > 0 && cfg.Args[0] == "build" {
			writeBuildOutput(t, cfg, "WASM")
			return runner.MockProcess(nil, nil), nil
		}
		return origHandler(cfg)
	}
	err := runReleaseWithRunner(mock)
	assert.Nil(t, err)

	// Verify no benchmark command was issued
	for _, cfg := range mock.Calls() {
		if cfg.IsCmd("go", "test") {
			assert.False(t, cfg.HasArg("-bench"), "should not have -bench flag when --no-benchmark is set")
		}
	}
}

func TestMatrixOutputShowsProgressAndDuration(t *testing.T) {
	fakeGoroot, _ := setupCosmoMatrixTest(t, []string{"wasm/js", "wasm/wasip1"})
	releaseParallel = 1

	mock := newTestPassMock(0)
	origHandler := mock.Handler
	forkGo := cosmoGoBinPath(fakeGoroot)
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.Name == forkGo && len(cfg.Args) > 0 && cfg.Args[0] == "build" {
			writeBuildOutput(t, cfg, "WASM")
			return runner.MockProcess(nil, nil), nil
		}
		return origHandler(cfg)
	}
	output := captureStdout(func() {
		err := runReleaseWithRunner(mock)
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
	fakeGoroot, _ := setupCosmoMatrixTest(t, []string{"wasm/js"})
	releaseParallel = 1

	mock := newTestPassMock(0)
	origHandler := mock.Handler
	forkGo := cosmoGoBinPath(fakeGoroot)
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.Name == forkGo && len(cfg.Args) > 0 && cfg.Args[0] == "build" {
			return nil, fmt.Errorf("build failed")
		}
		return origHandler(cfg)
	}

	output := captureStdout(func() {
		err := runReleaseWithRunner(mock)
		assert.NotNil(t, err)
	})

	// The FAIL line should show its progress counter and a duration (no parentheses)
	assert.Regexp(t, `FAIL \[1/1\].*\d+\.\d+s`, output)
}
