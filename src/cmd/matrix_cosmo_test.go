package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	// ("mytool"); without go.mod the output name degenerates to ".". The
	// go.mod also makes vet's package load real, so main.go must be
	// gofmt-canonical: in CI vet runs in check mode and would fail the run
	// (locally it would silently rewrite the fixture instead).
	os.WriteFile("go.mod", []byte("module example.com/mytool\n\ngo 1.21\n"), 0644)
	os.WriteFile("main.go", []byte("package main\n\nfunc main() {}\n"), 0644)

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
	// Pin the local (non-CI) shape: the fat name becomes a symlink to the
	// first slot copy. The CI shape (fat dropped) is pinned separately below.
	t.Setenv("CI", "")

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
	require.NoError(t, err)

	// The fat APE name and its three default slot copies must exist,
	// byte-identical; the fat name itself is a symlink to the first copy
	// (buildhost rejects os=cosmo uploads, so only the slots are published).
	// darwin/arm64 is NOT a default slot (macOS pipeline wedge — see
	// DefaultCosmoSlots).
	fatMatches, _ := filepath.Glob(filepath.Join(outDir, "*_cosmo_fat"))
	require.Len(t, fatMatches, 1, "expected exactly one fat APE in %s", outDir)
	name := strings.TrimSuffix(filepath.Base(fatMatches[0]), "_cosmo_fat")
	assert.NoFileExists(t, filepath.Join(outDir, name+"_darwin_arm64"),
		"darwin/arm64 must not receive an APE slot copy by default")
	for _, slotName := range []string{
		name + "_linux_amd64", name + "_linux_arm64",
		name + "_windows_amd64.exe",
	} {
		info, statErr := os.Lstat(filepath.Join(outDir, slotName))
		require.NoError(t, statErr, "slot copy %s must exist", slotName)
		assert.True(t, info.Mode().IsRegular(), "slot copy %s must be a regular file", slotName)
		data, readErr := os.ReadFile(filepath.Join(outDir, slotName))
		assert.Nil(t, readErr, "slot copy %s must exist", slotName)
		assert.Equal(t, "FAT-APE", string(data), "slot copy %s must be byte-identical to the APE", slotName)
	}
	linkTarget, err2 := os.Readlink(fatMatches[0])
	require.NoError(t, err2, "fat name must be a symlink locally")
	assert.Equal(t, name+"_linux_amd64", linkTarget)

	// checksums.txt covers the three real copies; the fat symlink is excluded
	// (checksums cover real files only).
	sums, err2 := os.ReadFile(filepath.Join(outDir, "checksums.txt"))
	assert.Nil(t, err2)
	sumLines := strings.Split(strings.TrimSpace(string(sums)), "\n")
	assert.Equal(t, 3, len(sumLines))
	assert.NotContains(t, string(sums), "_cosmo_fat")

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

func TestRunReleaseWithRunnerCosmoTargetCIDropsFat(t *testing.T) {
	fakeGoroot, outDir := setupCosmoMatrixTest(t, []string{"cosmo"})
	// Pin the CI shape: the fat name is REMOVED after slot mapping, so the
	// uploaded build/ directory holds only publishable per-platform names
	// (buildhost rejects os=cosmo; upload-artifact dereferences symlinks).
	t.Setenv("CI", "1")
	// The mocked pipeline runs with no cache credentials configured; keep the
	// in-CI cache-config validation from failing the run on such machines.
	t.Setenv("GO_TOOLCHAIN_CACHING_INTENTIONALLY_NOT_CONFIGURED", "1")

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

	var runErr error
	output := captureStdout(func() {
		runErr = runReleaseWithRunner(mock)
	})
	require.NoError(t, runErr)
	assert.Contains(t, output, "DROP ")

	fatMatches, _ := filepath.Glob(filepath.Join(outDir, "*_cosmo_fat"))
	assert.Empty(t, fatMatches, "the fat name must not exist in CI output")

	entries, err := os.ReadDir(outDir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		assert.True(t, e.Type().IsRegular(), "CI build dir must hold regular files only, got %s", e.Name())
		names = append(names, e.Name())
	}
	assert.Len(t, names, 4, "3 slot copies + checksums.txt, got %v", names)
	sums, err := os.ReadFile(filepath.Join(outDir, "checksums.txt"))
	require.NoError(t, err)
	assert.Equal(t, 3, len(strings.Split(strings.TrimSpace(string(sums)), "\n")))
	assert.NotContains(t, string(sums), "_cosmo_fat")
}

func TestRunReleaseWithRunnerCosmoNativeCollision(t *testing.T) {
	fakeGoroot, outDir := setupCosmoMatrixTest(t, []string{"cosmo", "linux/amd64"})
	t.Setenv("CI", "")

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

	var runErr error
	output := captureCombinedOutput(func() {
		runErr = runReleaseWithRunner(mock)
	})
	require.NoError(t, runErr)

	// The explicitly-built native linux/amd64 binary wins its filename; the
	// slot copy is skipped with a warning. The other two slots are copied.
	fatMatches, _ := filepath.Glob(filepath.Join(outDir, "*_cosmo_fat"))
	require.Len(t, fatMatches, 1)
	name := strings.TrimSuffix(filepath.Base(fatMatches[0]), "_cosmo_fat")

	data, err := os.ReadFile(filepath.Join(outDir, name+"_linux_amd64"))
	assert.Nil(t, err)
	assert.Equal(t, "NATIVE", string(data), "explicit native build must win the colliding slot name")
	assert.Contains(t, output, "SKIP "+name+"_linux_amd64")

	data, err = os.ReadFile(filepath.Join(outDir, name+"_linux_arm64"))
	assert.Nil(t, err)
	assert.Equal(t, "FAT-APE", string(data))

	// The fat symlink points at the first slot copy actually created
	// (linux/arm64 — linux/amd64 was lost to the native collision).
	linkTarget, err := os.Readlink(fatMatches[0])
	require.NoError(t, err)
	assert.Equal(t, name+"_linux_arm64", linkTarget)

	// checksums: native linux/amd64 + 2 slot copies (the fat symlink is
	// excluded — checksums cover real files only).
	sums, err := os.ReadFile(filepath.Join(outDir, "checksums.txt"))
	assert.Nil(t, err)
	assert.Equal(t, 3, len(strings.Split(strings.TrimSpace(string(sums)), "\n")))
	assert.NotContains(t, string(sums), "_cosmo_fat")
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
	require.NoError(t, err)

	entries, err := os.ReadDir(outDir)
	require.NoError(t, err)
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
