package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// writeBuildOutput creates the file named by a go build command's -o flag,
// simulating the compiler producing its output (content marks who built it).
func writeBuildOutput(t *testing.T, cfg runner.Config, content string) {
	t.Helper()
	for i, arg := range cfg.Args {
		if arg == "-o" && i+1 < len(cfg.Args) {
			assert.NoError(t, os.WriteFile(cfg.Args[i+1], []byte(content), 0755))
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

	// A named module so ResolveBuildTargets derives a real binary name
	// ("mytool"); without go.mod the output name degenerates to ".".
	os.WriteFile("go.mod", []byte("module example.com/mytool\n\ngo 1.21\n"), 0644)
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
