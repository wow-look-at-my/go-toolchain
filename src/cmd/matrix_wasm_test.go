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

func TestRunReleaseWithRunnerWasmTargets(t *testing.T) {
	fakeGoroot, outDir := setupCosmoMatrixTest(t, []string{"js/wasm", "wasip1/wasm", "linux/amd64"})
	t.Setenv("CI", "")

	mock := newTestPassMock(0)
	origHandler := mock.Handler
	forkGo := filepath.Join(fakeGoroot, "bin", "go")
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.Name == forkGo && len(cfg.Args) > 0 && cfg.Args[0] == "build" {
			writeBuildOutput(t, cfg, "WASM")
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

	// The build warns that wasm artifacts are excluded from buildhost
	// publishing — but not the wasm-only warning, since a native target is
	// in the same run.
	assert.Contains(t, output, "excluded from buildhost publishing")
	assert.NotContains(t, output, "every target is wasm")

	// Wasm artifacts carry the .wasm suffix and are ordinary regular files;
	// the native target coexists in the same run.
	for name, content := range map[string]string{
		"mytool_js_wasm.wasm":     "WASM",
		"mytool_wasip1_wasm.wasm": "WASM",
		"mytool_linux_amd64":      "NATIVE",
	} {
		info, statErr := os.Lstat(filepath.Join(outDir, name))
		require.NoError(t, statErr, "artifact %s must exist", name)
		assert.True(t, info.Mode().IsRegular(), "artifact %s must be a regular file", name)
		data, readErr := os.ReadFile(filepath.Join(outDir, name))
		require.NoError(t, readErr)
		assert.Equal(t, content, string(data), "artifact %s content", name)
	}

	// No cosmo machinery may run for wasm targets: no fat APE, no slot copies.
	fatMatches, _ := filepath.Glob(filepath.Join(outDir, "*_cosmo_fat"))
	assert.Empty(t, fatMatches, "wasm targets must not produce a cosmo fat artifact")
	assert.NoFileExists(t, filepath.Join(outDir, "mytool_linux_arm64"),
		"wasm targets must not trigger cosmo slot copies")

	// checksums.txt covers all three artifacts, wasm included.
	sums, err := os.ReadFile(filepath.Join(outDir, "checksums.txt"))
	require.NoError(t, err)
	assert.Equal(t, 3, len(strings.Split(strings.TrimSpace(string(sums)), "\n")))
	assert.Contains(t, string(sums), "mytool_js_wasm.wasm")
	assert.Contains(t, string(sums), "mytool_wasip1_wasm.wasm")

	// Each wasm build must run the fork's bin/go with GOOS/GOARCH pinned
	// explicitly (the fork defaults to GOOS=cosmo), GOTOOLCHAIN=local, GOROOT
	// and PATH pointing at the toolchain, and CGO_ENABLED forced to 0.
	seenGOOS := map[string]bool{}
	for _, cfg := range mock.Calls() {
		if cfg.Name != forkGo {
			continue
		}
		goos, _ := cfg.Env.Get("GOOS")
		seenGOOS[goos] = true
		goarch, _ := cfg.Env.Get("GOARCH")
		assert.Equal(t, "wasm", goarch, "GOARCH must be pinned to wasm for GOOS=%s", goos)
		toolchain, _ := cfg.Env.Get("GOTOOLCHAIN")
		assert.Equal(t, "local", toolchain)
		goroot, _ := cfg.Env.Get("GOROOT")
		assert.Equal(t, fakeGoroot, goroot)
		path, _ := cfg.Env.Get("PATH")
		assert.True(t, strings.HasPrefix(path, filepath.Join(fakeGoroot, "bin")), "PATH must be prefixed with the fork GOROOT/bin")
		cgo, _ := cfg.Env.Get("CGO_ENABLED")
		assert.Equal(t, "0", cgo)
	}
	assert.True(t, seenGOOS["js"], "expected a js/wasm build via the fork toolchain")
	assert.True(t, seenGOOS["wasip1"], "expected a wasip1/wasm build via the fork toolchain")

	// The native target must NOT be routed through the fork toolchain.
	var nativeCfg *runner.Config
	for _, cfg := range mock.Calls() {
		if cfg.IsCmd("go", "build") && cfg.Name != forkGo {
			c := cfg
			nativeCfg = &c
		}
	}
	if assert.NotNil(t, nativeCfg, "expected a native build with the go on PATH") {
		goroot, ok := nativeCfg.Env.Get("GOROOT")
		assert.False(t, ok && goroot == fakeGoroot, "native builds must not inherit the fork GOROOT")
	}
}

func TestRunReleaseWithRunnerWasmOnlySkipsSlotParsing(t *testing.T) {
	fakeGoroot, outDir := setupCosmoMatrixTest(t, []string{"js/wasm"})
	// An invalid --cosmo-slots value must be ignored when no cosmo target is
	// requested: slot parsing is a cosmo-only prerequisite.
	cosmoSlots = []string{"not-a-pair"}

	mock := newTestPassMock(0)
	origHandler := mock.Handler
	forkGo := filepath.Join(fakeGoroot, "bin", "go")
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.Name == forkGo && len(cfg.Args) > 0 && cfg.Args[0] == "build" {
			writeBuildOutput(t, cfg, "WASM")
			return runner.MockProcess(nil, nil), nil
		}
		return origHandler(cfg)
	}

	var runErr error
	output := captureCombinedOutput(func() {
		runErr = runReleaseWithRunner(mock)
	})
	require.NoError(t, runErr)
	assert.FileExists(t, filepath.Join(outDir, "mytool_js_wasm.wasm"))

	// A wasm-only run additionally warns that a buildhost publish step will
	// find nothing to publish (autorelease should be off).
	assert.Contains(t, output, "excluded from buildhost publishing")
	assert.Contains(t, output, "every target is wasm")
}

func TestRunReleaseWithRunnerWasmToolchainFailureFailsFast(t *testing.T) {
	setupCosmoMatrixTest(t, []string{"wasip1/wasm"})
	ensureCosmoToolchainFunc = func() (string, error) {
		return "", fmt.Errorf("no fork toolchain for you")
	}

	mock := newTestPassMock(0)
	err := runReleaseWithRunner(mock)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "no fork toolchain for you")
	// Fail-fast: the toolchain is resolved before the test phase runs.
	for _, cfg := range mock.Calls() {
		assert.False(t, cfg.IsCmd("go", "test"), "tests must not run when the fork toolchain is unavailable")
	}
}
