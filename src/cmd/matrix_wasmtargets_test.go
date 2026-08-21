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
	fakeGoroot, outDir := setupCosmoMatrixTest(t, []string{"js/wasm", "wasip1/wasm", "linux/amd64"})
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

	// No cosmo machinery may run for wasm targets. The APE now lands under the
	// plain name, which the host symlink also uses, so the manifest is what
	// tells the two apart: only a cosmo build writes one.
	assert.NoFileExists(t, filepath.Join(outDir, buildhostManifestName),
		"wasm targets must not produce a cosmo fat artifact")
	assert.NoFileExists(t, filepath.Join(outDir, "mytool_linux_arm64"),
		"no artifact may appear for a platform that was not requested")

	// The fork's wasm_exec.js is shipped alongside the js artifact — it must
	// byte-match the toolchain that built the wasm.
	harness, err := os.ReadFile(filepath.Join(outDir, "wasm_exec.js"))
	require.NoError(t, err, "wasm_exec.js must be copied into the output dir for js/wasm builds")
	assert.Equal(t, "// harness", string(harness))

	// checksums.txt covers all three artifacts plus wasm_exec.js.
	sums, err := os.ReadFile(filepath.Join(outDir, "checksums.txt"))
	require.NoError(t, err)
	assert.Equal(t, 4, len(strings.Split(strings.TrimSpace(string(sums)), "\n")))
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

	// The native target must NOT be routed through the fork toolchain — and
	// must NOT carry the fork's cache namespace (normal toolchains have
	// properly version-keyed tool IDs; their cache behavior stays untouched).
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
		assert.False(t, nativeCfg.Env.Contains(cache.KeyNamespaceEnv), "native builds must not set a cache namespace")
	}
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
	fakeGoroot, outDir := setupCosmoMatrixTest(t, []string{"js/wasm", "linux/amd64", "darwin/arm64"})
	t.Setenv("CI", "")
	// Alongside the unconstrained root main ("mytool"): a js&&wasm-only main
	// (the go-font-renderer shape) and a linux-only main.
	writeConstrainedMain(t, "cmd/wasmonly", "//go:build js && wasm")
	writeConstrainedMain(t, "cmd/linuxonly", "//go:build linux")

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

	err := runReleaseWithRunner(mock)
	require.NoError(t, err)

	// Each target builds exactly the mains visible under ITS build context.
	for _, name := range []string{
		"mytool_wasm_js", "wasmonly_wasm_js", // js/wasm: unconstrained + js&&wasm
		"mytool_linux_amd64", "linuxonly_linux_amd64", // linux: unconstrained + linux-only
		"mytool_darwin_arm64", // darwin: unconstrained only
	} {
		assert.FileExists(t, filepath.Join(outDir, name), "expected artifact %s", name)
	}
	for _, name := range []string{
		"linuxonly_wasm_js", "linuxonly_darwin_arm64", // linux-only main leaks nowhere
		"wasmonly_linux_amd64", "wasmonly_darwin_arm64", // js-only main is never attempted natively
		"wasmonly_wasm_wasip1", // and not for wasm GOOSes it is not guarded for either
	} {
		assert.NoFileExists(t, filepath.Join(outDir, name), "artifact %s must not be built", name)
	}

	// The memlimit guard (injected into HOST-context mains before discovery)
	// must have been cleaned up and never linger in the js-only main dir.
	assert.NoFileExists(t, filepath.Join("cmd", "wasmonly", "gomemlimit_gen.go"))
	assert.NoFileExists(t, filepath.Join("cmd", "linuxonly", "gomemlimit_gen.go"))
}

func TestRunReleaseWithRunnerTargetWithoutMainsSkipped(t *testing.T) {
	fakeGoroot, outDir := setupCosmoMatrixTest(t, []string{"js/wasm", "linux/amd64"})
	t.Setenv("CI", "")
	// Replace the unconstrained root main with a linux-only one: the js/wasm
	// target then has NO main packages and must be skipped with a warning,
	// while linux/amd64 still builds.
	require.NoError(t, os.Remove("main.go"))
	writeConstrainedMain(t, ".", "//go:build linux")

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

	assert.Contains(t, output, "no main packages found under GOOS=js GOARCH=wasm")
	assert.FileExists(t, filepath.Join(outDir, "mytool_linux_amd64"))
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

func TestRunReleaseWithRunnerWasmViaOsArchFlags(t *testing.T) {
	// The action inputs os: wasm / arch: js flow through --os/--arch. This
	// must behave exactly like --targets wasm/js: same fork toolchain, same
	// <name>_wasm_js artifact, and the same per-target main discovery (the
	// js&&wasm-guarded main is found even though --targets is unset).
	fakeGoroot, outDir := setupCosmoMatrixTest(t, nil)
	matrixOS, matrixArch = []string{"wasm"}, []string{"js"}
	t.Setenv("CI", "")
	writeConstrainedMain(t, "cmd/wasmonly", "//go:build js && wasm")

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
	assert.FileExists(t, filepath.Join(outDir, "wasmonly_wasm_js"),
		"per-target discovery must apply to wasm platforms on the --os/--arch path too")

	// Both builds went through the fork toolchain with GOOS=js GOARCH=wasm.
	forkBuilds := 0
	for _, cfg := range mock.Calls() {
		if cfg.Name != forkGo {
			continue
		}
		forkBuilds++
		goos, _ := cfg.Env.Get("GOOS")
		assert.Equal(t, "js", goos)
		goarch, _ := cfg.Env.Get("GOARCH")
		assert.Equal(t, "wasm", goarch)
	}
	assert.Equal(t, 2, forkBuilds)
}
