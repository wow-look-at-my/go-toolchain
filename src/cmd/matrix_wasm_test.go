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

	// The build warns that publishing wasm artifacts requires buildhost wasm
	// artifact support; the opt-out exclusion warning must NOT appear on the
	// default path.
	assert.Contains(t, output, "requires buildhost wasm artifact support")
	assert.NotContains(t, output, "excluded from buildhost publishing")

	// Wasm artifacts use buildhost's publishable os=wasm naming (order
	// swapped, no extension) and are ordinary regular files; the native
	// target coexists in the same run.
	for name, content := range map[string]string{
		"mytool_wasm_js":     "WASM",
		"mytool_wasm_wasip1": "WASM",
		"mytool_linux_amd64": "NATIVE",
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
	assert.Contains(t, string(sums), "mytool_wasm_js")
	assert.Contains(t, string(sums), "mytool_wasm_wasip1")

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

	err := runReleaseWithRunner(mock)
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(outDir, "mytool_wasm_js"))
}

func TestRunReleaseWithRunnerWasmPublishOptOut(t *testing.T) {
	fakeGoroot, outDir := setupCosmoMatrixTest(t, []string{"js/wasm"})
	// GO_TOOLCHAIN_WASM_PUBLISH=0: fall back to the excluded .wasm-suffixed
	// naming, which never reaches the buildhost publish upload set (for
	// consumers whose buildhost predates wasm artifact support).
	t.Setenv(wasmPublishEnv, "0")

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

	// The opt-out shape is built (and checksummed); the publishable name is
	// not produced.
	assert.FileExists(t, filepath.Join(outDir, "mytool_js_wasm.wasm"))
	assert.NoFileExists(t, filepath.Join(outDir, "mytool_wasm_js"))
	sums, err := os.ReadFile(filepath.Join(outDir, "checksums.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(sums), "mytool_js_wasm.wasm")

	// The exclusion warning fires, plus the wasm-only note that a buildhost
	// publish step will find nothing to publish.
	assert.Contains(t, output, "excluded from buildhost publishing")
	assert.Contains(t, output, "every target is wasm")
	assert.NotContains(t, output, "requires buildhost wasm artifact support")
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
