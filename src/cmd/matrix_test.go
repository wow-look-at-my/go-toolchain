package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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
	err := runReleaseWithRunner(mock)
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
	err := runReleaseWithRunner(mock)
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
	err := runReleaseWithRunner(mock)
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
	err := runReleaseWithRunner(mock)
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
	err := runReleaseWithRunner(mock)
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
	err := runReleaseWithRunner(mock)
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
	err := runReleaseWithRunner(mock)
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
		err := runReleaseWithRunner(mock)
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
		err := runReleaseWithRunner(mock)
		assert.NotNil(t, err)
	})

	// FAIL line should show [1/1] counter and duration (no parentheses)
	assert.Regexp(t, `FAIL \[1/1\].*\d+\.\d+s`, output)
}

// writeBuildOutput creates the file named by a go build command's -o flag,
// simulating the compiler producing its output (content marks who built it).
func writeBuildOutput(t *testing.T, cfg runner.Config, content string) {
	t.Helper()
	for i, arg := range cfg.Args {
		if arg == "-o" && i+1 < len(cfg.Args) {
			if err := os.WriteFile(cfg.Args[i+1], []byte(content), 0755); err != nil {
				t.Errorf("writing mock build output %s: %v", cfg.Args[i+1], err)
			}
		}
	}
}

// setupCosmoMatrixTest points the matrix flags at the given targets, stubs
// the cosmo toolchain resolution, and restores everything on cleanup. It
// returns the fake GOROOT and the output directory.
func setupCosmoMatrixTest(t *testing.T, targets []string) (fakeGoroot, outDir string) {
	t.Helper()
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(oldWd) })

	os.WriteFile("main.go", []byte("package main\nfunc main() {}\n"), 0644)

	fakeGoroot = filepath.Join(tmpDir, "fake-cosmo-goroot")
	outDir = filepath.Join(tmpDir, "dist")

	oldTargets, oldSlots := matrixTargets, cosmoSlots
	oldOS, oldArch := matrixOS, matrixArch
	oldOutput, oldParallel, oldBench := outputDir, releaseParallel, noBenchmark
	oldEnsure := ensureCosmoToolchainFunc
	matrixTargets = targets
	cosmoSlots = DefaultCosmoSlots
	matrixOS, matrixArch = DefaultOS, DefaultArch
	outputDir = outDir
	releaseParallel = 1
	noBenchmark = true
	ensureCosmoToolchainFunc = func() (string, error) { return fakeGoroot, nil }
	t.Cleanup(func() {
		matrixTargets, cosmoSlots = oldTargets, oldSlots
		matrixOS, matrixArch = oldOS, oldArch
		outputDir, releaseParallel, noBenchmark = oldOutput, oldParallel, oldBench
		ensureCosmoToolchainFunc = oldEnsure
	})
	return fakeGoroot, outDir
}

func TestRunReleaseWithRunnerCosmoTarget(t *testing.T) {
	fakeGoroot, outDir := setupCosmoMatrixTest(t, []string{"cosmo"})

	mock := newTestPassMock(0)
	origHandler := mock.Handler
	cosmoGo := filepath.Join(fakeGoroot, "bin", "go")
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.Name == cosmoGo && len(cfg.Args) > 0 && cfg.Args[0] == "build" {
			writeBuildOutput(t, cfg, "FAT-APE")
			return runner.MockProcess(nil, nil), nil
		}
		return origHandler(cfg)
	}

	err := runReleaseWithRunner(mock)
	assert.Nil(t, err)

	// The fat APE and its four default slot copies must exist, byte-identical.
	fatMatches, _ := filepath.Glob(filepath.Join(outDir, "*_cosmo_fat"))
	assert.Equal(t, 1, len(fatMatches), "expected exactly one fat APE in %s", outDir)
	name := strings.TrimSuffix(filepath.Base(fatMatches[0]), "_cosmo_fat")
	for _, slotName := range []string{
		name + "_linux_amd64", name + "_linux_arm64",
		name + "_darwin_arm64", name + "_windows_amd64.exe",
	} {
		data, readErr := os.ReadFile(filepath.Join(outDir, slotName))
		assert.Nil(t, readErr, "slot copy %s must exist", slotName)
		assert.Equal(t, "FAT-APE", string(data), "slot copy %s must be byte-identical to the APE", slotName)
	}

	// checksums.txt covers the APE plus all four copies.
	sums, err2 := os.ReadFile(filepath.Join(outDir, "checksums.txt"))
	assert.Nil(t, err2)
	assert.Equal(t, 5, len(strings.Split(strings.TrimSpace(string(sums)), "\n")))

	// The cosmo build must run the gosmopolitan go with the fat-APE env:
	// GOOS=cosmo, no GOARCH, GOTOOLCHAIN=local, GOROOT + PATH pointing at the
	// toolchain, and CGO_ENABLED forced to 0.
	var cosmoCfg *runner.Config
	for _, cfg := range mock.Calls() {
		if cfg.Name == cosmoGo {
			c := cfg
			cosmoCfg = &c
		}
	}
	if assert.NotNil(t, cosmoCfg, "expected a build via the cosmo toolchain's bin/go") {
		goos, _ := cosmoCfg.Env.Get("GOOS")
		assert.Equal(t, "cosmo", goos)
		goarch, _ := cosmoCfg.Env.Get("GOARCH")
		assert.Equal(t, "", goarch, "GOARCH must be cleared for the fat build")
		gocosmofat, _ := cosmoCfg.Env.Get("GOCOSMOFAT")
		assert.Equal(t, "", gocosmofat, "GOCOSMOFAT must be cleared for the fat build")
		toolchain, _ := cosmoCfg.Env.Get("GOTOOLCHAIN")
		assert.Equal(t, "local", toolchain)
		goroot, _ := cosmoCfg.Env.Get("GOROOT")
		assert.Equal(t, fakeGoroot, goroot)
		path, _ := cosmoCfg.Env.Get("PATH")
		assert.True(t, strings.HasPrefix(path, filepath.Join(fakeGoroot, "bin")), "PATH must be prefixed with the cosmo GOROOT/bin")
		cgo, _ := cosmoCfg.Env.Get("CGO_ENABLED")
		assert.Equal(t, "0", cgo)
	}
}

func TestRunReleaseWithRunnerCosmoNativeCollision(t *testing.T) {
	fakeGoroot, outDir := setupCosmoMatrixTest(t, []string{"cosmo", "linux/amd64"})

	mock := newTestPassMock(0)
	origHandler := mock.Handler
	cosmoGo := filepath.Join(fakeGoroot, "bin", "go")
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.Name == cosmoGo && len(cfg.Args) > 0 && cfg.Args[0] == "build" {
			writeBuildOutput(t, cfg, "FAT-APE")
			return runner.MockProcess(nil, nil), nil
		}
		if cfg.IsCmd("go", "build") {
			writeBuildOutput(t, cfg, "NATIVE")
		}
		return origHandler(cfg)
	}

	output := captureStdout(func() {
		err := runReleaseWithRunner(mock)
		assert.Nil(t, err)
	})

	// The explicitly-built native linux/amd64 binary wins its filename; the
	// slot copy is skipped with a warning. The other three slots are copied.
	fatMatches, _ := filepath.Glob(filepath.Join(outDir, "*_cosmo_fat"))
	assert.Equal(t, 1, len(fatMatches))
	name := strings.TrimSuffix(filepath.Base(fatMatches[0]), "_cosmo_fat")

	data, err := os.ReadFile(filepath.Join(outDir, name+"_linux_amd64"))
	assert.Nil(t, err)
	assert.Equal(t, "NATIVE", string(data), "explicit native build must win the colliding slot name")
	assert.Contains(t, output, "SKIP "+name+"_linux_amd64")

	data, err = os.ReadFile(filepath.Join(outDir, name+"_linux_arm64"))
	assert.Nil(t, err)
	assert.Equal(t, "FAT-APE", string(data))

	// checksums: native linux/amd64 + fat APE + 3 slot copies.
	sums, err := os.ReadFile(filepath.Join(outDir, "checksums.txt"))
	assert.Nil(t, err)
	assert.Equal(t, 5, len(strings.Split(strings.TrimSpace(string(sums)), "\n")))
}

func TestRunReleaseWithRunnerCosmoSlotsNone(t *testing.T) {
	fakeGoroot, outDir := setupCosmoMatrixTest(t, []string{"cosmo"})
	cosmoSlots = []string{"none"}

	mock := newTestPassMock(0)
	origHandler := mock.Handler
	cosmoGo := filepath.Join(fakeGoroot, "bin", "go")
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.Name == cosmoGo && len(cfg.Args) > 0 && cfg.Args[0] == "build" {
			writeBuildOutput(t, cfg, "FAT-APE")
			return runner.MockProcess(nil, nil), nil
		}
		return origHandler(cfg)
	}

	err := runReleaseWithRunner(mock)
	assert.Nil(t, err)

	entries, err := os.ReadDir(outDir)
	assert.Nil(t, err)
	var regularFiles []string
	for _, e := range entries {
		if e.Type().IsRegular() {
			regularFiles = append(regularFiles, e.Name())
		}
	}
	// Just the fat APE and its checksum file; no slot copies.
	assert.Equal(t, 2, len(regularFiles), "got %v", regularFiles)
	for _, f := range regularFiles {
		assert.True(t, strings.HasSuffix(f, "_cosmo_fat") || f == "checksums.txt", "unexpected file %s", f)
	}
}

func TestRunReleaseWithRunnerCosmoToolchainFailureFailsFast(t *testing.T) {
	setupCosmoMatrixTest(t, []string{"cosmo"})
	ensureCosmoToolchainFunc = func() (string, error) {
		return "", fmt.Errorf("no toolchain for you")
	}

	mock := newTestPassMock(0)
	err := runReleaseWithRunner(mock)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "no toolchain for you")
	// Fail-fast: the toolchain is resolved before the test phase runs.
	for _, cfg := range mock.Calls() {
		assert.False(t, cfg.IsCmd("go", "test"), "tests must not run when the cosmo toolchain is unavailable")
	}
}

func TestRunReleaseWithRunnerInvalidTargets(t *testing.T) {
	oldTargets := matrixTargets
	matrixTargets = []string{"cosmo/amd64"}
	defer func() { matrixTargets = oldTargets }()

	mock := newTestPassMock(0)
	err := runReleaseWithRunner(mock)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "fat APE")
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
