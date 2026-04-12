package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/go-toolchain/src/build"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

func TestRunReleaseWithRunnerNoPlatforms(t *testing.T) {
	oldOS := matrixOS
	oldArch := matrixArch
	matrixOS = []string{}
	matrixArch = []string{}
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
	}()

	mock := runner.NewMock()
	err := runReleaseWithRunner(mock, nil)
	assert.NotNil(t, err)
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

func TestRunBuildCapturesStderr(t *testing.T) {
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "build") {
			return runner.MockProcessWithStderr(nil, []byte("./main.go:5:3: undefined: foo\n"), fmt.Errorf("exit status 1")), nil
		}
		return nil, nil
	}

	job := buildJob{
		goos:       "linux",
		goarch:     "amd64",
		srcPath:    ".",
		outputPath: "/tmp/test",
	}

	err := runBuild(mock, job, nil)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "exit status 1")
	assert.Contains(t, err.Error(), "undefined: foo")
}

func TestRunBuildNoStderrOnSuccess(t *testing.T) {
	mock := runner.NewMock()
	job := buildJob{
		goos:       "linux",
		goarch:     "amd64",
		srcPath:    ".",
		outputPath: "/tmp/test",
	}

	err := runBuild(mock, job, nil)
	assert.Nil(t, err)
}

func TestRunBuild(t *testing.T) {
	oldCgo := cgoEnabled
	cgoEnabled = false
	defer func() { cgoEnabled = oldCgo }()

	mock := runner.NewMock()
	job := buildJob{
		goos:       "linux",
		goarch:     "amd64",
		srcPath:    ".",
		outputPath: "/tmp/test",
	}

	err := runBuild(mock, job, nil)
	assert.Nil(t, err)

	// Verify command was called
	calls := mock.Calls()
	assert.Equal(t, 1, len(calls))

	// Verify env vars were set
	cfg := calls[0]
	goos, _ := cfg.Env.Get("GOOS")
	goarch, _ := cfg.Env.Get("GOARCH")
	cgo, _ := cfg.Env.Get("CGO_ENABLED")
	assert.Equal(t, "linux", goos)
	assert.Equal(t, "amd64", goarch)
	assert.Equal(t, "0", cgo)

	// Verify -o flag
	hasOutput := false
	for i, arg := range cfg.Args {
		if arg == "-o" && i+1 < len(cfg.Args) {
			hasOutput = strings.Contains(cfg.Args[i+1], "/tmp/test")
		}
	}
	assert.True(t, hasOutput)
}

func TestRunBuildWithCgoEnabled(t *testing.T) {
	oldCgo := cgoEnabled
	cgoEnabled = true
	defer func() { cgoEnabled = oldCgo }()

	mock := runner.NewMock()
	job := buildJob{
		goos:       "linux",
		goarch:     "amd64",
		srcPath:    ".",
		outputPath: "/tmp/test",
	}

	err := runBuild(mock, job, nil)
	assert.Nil(t, err)

	calls := mock.Calls()
	assert.Equal(t, 1, len(calls))

	cfg := calls[0]
	goos2, _ := cfg.Env.Get("GOOS")
	goarch2, _ := cfg.Env.Get("GOARCH")
	assert.Equal(t, "linux", goos2)
	assert.Equal(t, "amd64", goarch2)
	hasCgo := cfg.Env.Contains("CGO_ENABLED")
	assert.False(t, hasCgo, "CGO_ENABLED should not be set when --cgo is used")
}

func TestCreateHostSymlinks(t *testing.T) {
	tmpDir := t.TempDir()

	targets := []build.Target{
		{ImportPath: "./cmd/mytool", OutputName: "mytool"},
	}

	// Create a fake host binary
	hostBinary := fmt.Sprintf("mytool_%s_%s", runtime.GOOS, runtime.GOARCH)
	os.WriteFile(filepath.Join(tmpDir, hostBinary), []byte("binary"), 0755)

	err := createHostSymlinks(targets, tmpDir)
	assert.Nil(t, err)

	// Check _host symlink
	linkTarget, err := os.Readlink(filepath.Join(tmpDir, "mytool_host"))
	assert.Nil(t, err)
	assert.Equal(t, hostBinary, linkTarget)

	// Check bare symlink
	linkTarget, err = os.Readlink(filepath.Join(tmpDir, "mytool"))
	assert.Nil(t, err)
	assert.Equal(t, hostBinary, linkTarget)
}

func TestCreateHostSymlinksSkipsMissing(t *testing.T) {
	tmpDir := t.TempDir()

	targets := []build.Target{
		{ImportPath: "./cmd/mytool", OutputName: "mytool"},
	}

	// Don't create the host binary — should skip without error
	err := createHostSymlinks(targets, tmpDir)
	assert.Nil(t, err)

	// Symlinks should not exist
	_, err = os.Readlink(filepath.Join(tmpDir, "mytool_host"))
	assert.NotNil(t, err)
	_, err = os.Readlink(filepath.Join(tmpDir, "mytool"))
	assert.NotNil(t, err)
}

func TestRunReleaseWithRunnerRunsBenchmarks(t *testing.T) {
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

func TestCreateHostSymlinksReplacesStale(t *testing.T) {
	tmpDir := t.TempDir()

	targets := []build.Target{
		{ImportPath: "./cmd/mytool", OutputName: "mytool"},
	}

	hostBinary := fmt.Sprintf("mytool_%s_%s", runtime.GOOS, runtime.GOARCH)
	os.WriteFile(filepath.Join(tmpDir, hostBinary), []byte("binary"), 0755)

	// Create stale symlinks pointing elsewhere
	os.Symlink("old_target", filepath.Join(tmpDir, "mytool_host"))
	os.Symlink("old_target", filepath.Join(tmpDir, "mytool"))

	err := createHostSymlinks(targets, tmpDir)
	assert.Nil(t, err)

	linkTarget, _ := os.Readlink(filepath.Join(tmpDir, "mytool_host"))
	assert.Equal(t, hostBinary, linkTarget)
	linkTarget, _ = os.Readlink(filepath.Join(tmpDir, "mytool"))
	assert.Equal(t, hostBinary, linkTarget)
}
