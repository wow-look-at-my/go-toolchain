package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/cache"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// writeFakeForkGoroot writes the given relative-path → content files under
// root, creating parent directories. Used to build fake gosmopolitan GOROOTs
// that forkToolchainCacheNamespace can fingerprint.
func writeFakeForkGoroot(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0755))
	}
}

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

	// Populate the fake GOROOT with the minimal tool-binary layout so the
	// REAL forkToolchainCacheNamespace fingerprints it (no seam: production
	// always hashes the toolchain it is about to build with).
	writeFakeForkGoroot(t, fakeGoroot, map[string]string{
		"VERSION":                      "go1.26.4cosmo",
		"bin/go":                       "fake go binary",
		"pkg/tool/linux_amd64/compile": "fake compile binary",
		"pkg/tool/linux_amd64/link":    "fake link binary",
	})

	oldTargets, oldPlatforms := matrixTargets, cosmoPlatforms
	oldOutput, oldParallel, oldBench := outputDir, releaseParallel, noBenchmark
	oldEnsure, oldSupported := ensureCosmoToolchainFunc, cosmoPlatformsSupportedFunc
	matrixTargets = targets
	cosmoPlatforms = DefaultCosmoPlatforms
	outputDir = outDir
	releaseParallel = 1
	noBenchmark = true
	ensureCosmoToolchainFunc = func() (string, error) { return fakeGoroot, nil }
	// The fake GOROOT's bin/go is not executable, so the real probe would
	// report "unsupported" and every cosmo test would carry its warning.
	cosmoPlatformsSupportedFunc = func(string) bool { return true }
	t.Cleanup(func() {
		matrixTargets, cosmoPlatforms = oldTargets, oldPlatforms
		outputDir, releaseParallel, noBenchmark = oldOutput, oldParallel, oldBench
		ensureCosmoToolchainFunc, cosmoPlatformsSupportedFunc = oldEnsure, oldSupported
	})
	return fakeGoroot, outDir
}

// A cosmo build produces ONE file. This pins that outcome from the outside --
// the build directory itself -- rather than from any flag: a copy of the APE
// under a per-platform name is a thing this repo can no longer express.
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
	require.NoError(t, err)

	// The whole build directory. Exactly one binary exists as a file: the APE,
	// under the plain name. The convenience names are symlinks TO it -- the
	// distinction the deleted slot copier erased, since a copy is a second
	// binary and a link is not.
	var manifest buildhostManifest
	manifestRaw, manifestErr := os.ReadFile(filepath.Join(outDir, buildhostManifestName))
	require.NoError(t, manifestErr)
	require.NoError(t, json.Unmarshal(manifestRaw, &manifest))
	require.Len(t, manifest.Artifacts, 1)
	fatName := manifest.Artifacts[0].File
	name := manifest.Artifacts[0].Filename
	require.FileExists(t, filepath.Join(outDir, fatName))

	entries, readErr := os.ReadDir(outDir)
	require.NoError(t, readErr)
	var files []string
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			target, linkErr := os.Readlink(filepath.Join(outDir, e.Name()))
			require.NoError(t, linkErr)
			assert.Equal(t, fatName, target, "%s must link to the APE, not duplicate it", e.Name())
			continue
		}
		files = append(files, e.Name())
	}
	assert.ElementsMatch(t, []string{
		fatName, "checksums.txt", buildhostManifestName,
	}, files, "a cosmo build writes one APE plus its checksums and manifest")

	// The manifest carries the platform set the filename grammar cannot spell.
	// The APE lands under the plain name, so the file IS the served filename.
	assert.Equal(t, name, manifest.Artifacts[0].File)
	assert.Equal(t, DefaultCosmoPlatforms, manifest.Artifacts[0].Platforms)

	// checksums.txt covers the one real file.
	sums, err2 := os.ReadFile(filepath.Join(outDir, "checksums.txt"))
	assert.Nil(t, err2)
	sumLines := strings.Split(strings.TrimSpace(string(sums)), "\n")
	assert.Equal(t, 1, len(sumLines))
	assert.Contains(t, string(sums), fatName)

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
		// Cache isolation: the cosmo build env must carry the cache key
		// namespace derived from THIS toolchain's content, so its cacheprog
		// can never share cache entries with a different fork toolchain build
		// (the 2026-07-20 cross-build poisoning).
		wantNS, nsErr := forkToolchainCacheNamespace(fakeGoroot)
		require.NoError(t, nsErr)
		require.NotEmpty(t, wantNS)
		ns, _ := cosmoCfg.Env.Get(cache.KeyNamespaceEnv)
		assert.Equal(t, wantNS, ns, "cosmo build env must set %s from the toolchain content hash", cache.KeyNamespaceEnv)
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

// TestRunBuildForkEnvSetsCacheNamespace: runBuild must export the job's cache
// namespace into the fork build's environment — for BOTH fork shapes (cosmo
// fat APE and wasm) — so the spawned cacheprog scopes every cache key to the
// toolchain build.
func TestRunBuildForkEnvSetsCacheNamespace(t *testing.T) {
	for _, tc := range []struct {
		name   string
		goos   string
		goarch string
	}{
		{"cosmo", cosmoOS, cosmoFatArch},
		{"wasm", "js", "wasm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := runner.NewMock()
			job := buildJob{
				goos:           tc.goos,
				goarch:         tc.goarch,
				srcPath:        ".",
				outputPath:     filepath.Join(t.TempDir(), "out"),
				forkGoroot:     filepath.Join(t.TempDir(), "fork-goroot"),
				cacheNamespace: "deadbeef00c0ffee",
			}
			require.NoError(t, runBuild(mock, job, nil))

			calls := mock.Calls()
			require.Len(t, calls, 1)
			ns, ok := calls[0].Env.Get(cache.KeyNamespaceEnv)
			assert.True(t, ok, "%s must be set on the fork build env", cache.KeyNamespaceEnv)
			assert.Equal(t, "deadbeef00c0ffee", ns)
		})
	}
}

// TestRunBuildForkWithoutNamespaceRefuses: the last-chokepoint guard — a
// fork-toolchain job with no cache namespace must not build at all. A call
// site that forgot to fingerprint the toolchain fails loudly instead of
// silently sharing the un-namespaced cache across toolchain builds.
func TestRunBuildForkWithoutNamespaceRefuses(t *testing.T) {
	mock := runner.NewMock()
	job := buildJob{
		goos:       cosmoOS,
		goarch:     cosmoFatArch,
		srcPath:    ".",
		outputPath: filepath.Join(t.TempDir(), "out"),
		forkGoroot: filepath.Join(t.TempDir(), "fork-goroot"),
	}
	err := runBuild(mock, job, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cache namespace")
	assert.Empty(t, mock.Calls(), "no build may run without the namespace")
}

// TestRunBuildNonForkEnvHasNoNamespace: normal (non-fork) jobs keep their
// cache behavior byte-identical — no namespace variable in their env.
func TestRunBuildNonForkEnvHasNoNamespace(t *testing.T) {
	mock := runner.NewMock()
	job := buildJob{
		goos:       "linux",
		goarch:     "amd64",
		srcPath:    ".",
		outputPath: filepath.Join(t.TempDir(), "out"),
	}
	require.NoError(t, runBuild(mock, job, nil))
	calls := mock.Calls()
	require.Len(t, calls, 1)
	if calls[0].Env != nil {
		assert.False(t, calls[0].Env.Contains(cache.KeyNamespaceEnv), "non-fork builds must not set a cache namespace")
	}
}
