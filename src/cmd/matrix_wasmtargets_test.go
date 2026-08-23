package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/cache"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

func TestRunReleaseWithRunnerWasmTargets(t *testing.T) {
	fakeGoroot, outDir := setupCosmoMatrixTest(t, []string{"js/wasm", "wasip1/wasm"})
	t.Setenv("CI", "")
	// The fork toolchain ships the js exec harness; a js/wasm build copies it
	// next to the artifact.
	require.NoError(t, os.MkdirAll(filepath.Join(fakeGoroot, "lib", "wasm"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(fakeGoroot, "lib", "wasm", "wasm_exec.js"), []byte("// harness"), 0644))

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

	// The build warns that publishing wasm artifacts requires buildhost wasm
	// artifact support; the opt-out exclusion warning must NOT appear on the
	// default path.
	assert.Contains(t, output, "requires buildhost wasm artifact support")
	assert.NotContains(t, output, "excluded from buildhost publishing")

	// Wasm artifacts use buildhost's publishable os=wasm naming (order
	// swapped, no extension) and are ordinary regular files.
	for name, content := range map[string]string{
		"mytool_wasm_js":     "WASM",
		"mytool_wasm_wasip1": "WASM",
	} {
		info, statErr := os.Lstat(filepath.Join(outDir, name))
		require.NoError(t, statErr, "artifact %s must exist", name)
		assert.True(t, info.Mode().IsRegular(), "artifact %s must be a regular file", name)
		data, readErr := os.ReadFile(filepath.Join(outDir, name))
		require.NoError(t, readErr)
		assert.Equal(t, content, string(data), "artifact %s content", name)
	}

	// No cosmo machinery may run for wasm-only targets. The APE now lands
	// under the plain name, which the host symlink also uses, so the
	// manifest is what tells the two apart: only a cosmo build writes one.
	assert.NoFileExists(t, filepath.Join(outDir, buildhostManifestName),
		"wasm targets must not produce a cosmo fat artifact")
	assert.NoFileExists(t, filepath.Join(outDir, "mytool_linux_arm64"),
		"no artifact may appear for a platform that was not requested")

	// The fork's wasm_exec.js is shipped alongside the js artifact — it must
	// byte-match the toolchain that built the wasm.
	harness, err := os.ReadFile(filepath.Join(outDir, "wasm_exec.js"))
	require.NoError(t, err, "wasm_exec.js must be copied into the output dir for js/wasm builds")
	assert.Equal(t, "// harness", string(harness))

	// checksums.txt covers both wasm artifacts plus wasm_exec.js.
	sums, err := os.ReadFile(filepath.Join(outDir, "checksums.txt"))
	require.NoError(t, err)
	assert.Equal(t, 3, len(strings.Split(strings.TrimSpace(string(sums)), "\n")))
	assert.Contains(t, string(sums), "mytool_wasm_js")
	assert.Contains(t, string(sums), "mytool_wasm_wasip1")
	assert.Contains(t, string(sums), "wasm_exec.js")

	// Each wasm build must run the fork's bin/go with GOOS/GOARCH pinned
	// explicitly (the fork defaults to GOOS=cosmo), GOTOOLCHAIN=local, GOROOT
	// and PATH pointing at the toolchain, and CGO_ENABLED forced to 0.
	seenGOOS := set.New[string]()
	for _, cfg := range mock.Calls() {
		if cfg.Name != forkGo {
			continue
		}
		goos, _ := cfg.Env.Get("GOOS")
		seenGOOS.Add(goos)
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
		// Wasm builds use the same constant-version fork, so they carry the
		// same toolchain-content cache namespace as the cosmo build.
		ns, _ := cfg.Env.Get(cache.KeyNamespaceEnv)
		wantNS, nsErr := forkToolchainCacheNamespace(fakeGoroot)
		require.NoError(t, nsErr)
		assert.Equal(t, wantNS, ns, "wasm build env must set %s from the toolchain content hash", cache.KeyNamespaceEnv)
	}
	assert.True(t, seenGOOS.Contains("js"), "expected a js/wasm build via the fork toolchain")
	assert.True(t, seenGOOS.Contains("wasip1"), "expected a wasip1/wasm build via the fork toolchain")
}

func TestRunReleaseWithRunnerWasmOnlySkipsCosmoPrereqs(t *testing.T) {
	// Uses the canonical wasm/js spelling end to end (the other wasm tests
	// pin the js/wasm GOOS-order alias); both produce the same artifact.
	fakeGoroot, outDir := setupCosmoMatrixTest(t, []string{"wasm/js"})
	// An invalid --cosmo-platforms value must be ignored when no cosmo target
	// is requested: it is a cosmo-only prerequisite. A wasm-only build also
	// writes no manifest -- the manifest exists to publish an APE.
	cosmoPlatforms = []string{"not-a-pair"}

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
	assert.NoFileExists(t, filepath.Join(outDir, buildhostManifestName))
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

// writeConstrainedMain adds a nested main package with the given build
// constraint line ("" for unconstrained) to the module set up by
// setupCosmoMatrixTest (which chdirs into the module root).
func writeConstrainedMain(t *testing.T, dir, constraint string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0755))
	src := "package main\n\nfunc main() {}\n"
	if constraint != "" {
		src = constraint + "\n\n" + src
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0644))
}

func TestRunReleaseWithRunnerPerTargetMainDiscovery(t *testing.T) {
	fakeGoroot, outDir := setupCosmoMatrixTest(t, []string{"js/wasm", "wasip1/wasm"})
	t.Setenv("CI", "")
	// Alongside the unconstrained root main ("mytool"): a js&&wasm-only main
	// (the go-font-renderer shape) and a wasip1&&wasm-only main.
	writeConstrainedMain(t, "cmd/wasmonly", "//go:build js && wasm")
	writeConstrainedMain(t, "cmd/wasip1only", "//go:build wasip1 && wasm")

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

	// Each target builds exactly the mains visible under ITS build context.
	for _, name := range []string{
		"mytool_wasm_js", "wasmonly_wasm_js", // js/wasm: unconstrained + js&&wasm
		"mytool_wasm_wasip1", "wasip1only_wasm_wasip1", // wasip1/wasm: unconstrained + wasip1&&wasm
	} {
		assert.FileExists(t, filepath.Join(outDir, name), "expected artifact %s", name)
	}
	for _, name := range []string{
		"wasip1only_wasm_js",   // wasip1-only main leaks nowhere else
		"wasmonly_wasm_wasip1", // js-only main is never attempted for wasip1
	} {
		assert.NoFileExists(t, filepath.Join(outDir, name), "artifact %s must not be built", name)
	}

	// The memlimit guard (injected into HOST-context mains before discovery)
	// must have been cleaned up and never linger in the wasm-only main dirs.
	assert.NoFileExists(t, filepath.Join("cmd", "wasmonly", "gomemlimit_gen.go"))
	assert.NoFileExists(t, filepath.Join("cmd", "wasip1only", "gomemlimit_gen.go"))
}

func TestRunReleaseWithRunnerTargetWithoutMainsSkipped(t *testing.T) {
	fakeGoroot, outDir := setupCosmoMatrixTest(t, []string{"js/wasm", "wasip1/wasm"})
	t.Setenv("CI", "")
	// Replace the unconstrained root main with a wasip1-only one: the js/wasm
	// target then has NO main packages and must be skipped with a warning,
	// while wasip1/wasm still builds.
	require.NoError(t, os.Remove("main.go"))
	writeConstrainedMain(t, ".", "//go:build wasip1 && wasm")

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

	assert.Contains(t, output, "no main packages found under GOOS=js GOARCH=wasm")
	assert.FileExists(t, filepath.Join(outDir, "mytool_wasm_wasip1"))
	assert.NoFileExists(t, filepath.Join(outDir, "mytool_wasm_js"))
	// No js artifact was built, so the harness is not shipped either.
	assert.NoFileExists(t, filepath.Join(outDir, "wasm_exec.js"))
}

func TestRunReleaseWithRunnerNoMainsForAnyTargetFails(t *testing.T) {
	_, _ = setupCosmoMatrixTest(t, []string{"js/wasm"})
	// Only a linux-guarded main: the js/wasm-only target list has nothing to
	// build anywhere, which is an error (matching the historic behavior).
	require.NoError(t, os.Remove("main.go"))
	writeConstrainedMain(t, ".", "//go:build linux")

	mock := newTestPassMock(0)
	var runErr error
	captureCombinedOutput(func() {
		runErr = runReleaseWithRunner(mock)
	})
	require.Error(t, runErr)
	assert.Contains(t, runErr.Error(), "no main packages found to build")
}
