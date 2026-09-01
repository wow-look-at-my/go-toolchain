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

// stubForkToolchain writes a fake gosmopolitan GOROOT with the minimal
// tool-binary layout the REAL forkToolchainCacheNamespace can fingerprint (no
// seam there: production always hashes the toolchain it is about to build
// with), and points toolchain resolution at it. Every build path resolves the
// fork, so any test reaching the build phase needs this.
func stubForkToolchain(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "fake-cosmo-goroot")
	writeFakeForkGoroot(t, root, map[string]string{
		"VERSION":                      "go1.26.4cosmo",
		"bin/go":                       "fake go binary",
		"pkg/tool/linux_amd64/compile": "fake compile binary",
		"pkg/tool/linux_amd64/link":    "fake link binary",
	})
	oldEnsure, oldSupported := ensureCosmoToolchainFunc, cosmoPlatformsSupportedFunc
	ensureCosmoToolchainFunc = func() (string, error) { return root, nil }
	// The fake GOROOT's bin/go is not executable, so the real probe would report "unsupported".
	cosmoPlatformsSupportedFunc = func(string) bool { return true }
	t.Cleanup(func() {
		ensureCosmoToolchainFunc, cosmoPlatformsSupportedFunc = oldEnsure, oldSupported
	})
	return root
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

	// A named module so ResolveBuildTargets derives a real binary name; main.go must stay gofmt-canonical for vet's check mode in CI.
	os.WriteFile("go.mod", []byte("module example.com/mytool\n\ngo 1.21\n"), 0644)
	os.WriteFile("main.go", []byte("package main\n\nfunc main() {}\n"), 0644)

	fakeGoroot = stubForkToolchain(t)
	outDir = filepath.Join(tmpDir, "dist")

	oldTargets, oldPlatforms := matrixTargets, cosmoPlatforms
	oldOutput, oldParallel, oldBench := outputDir, releaseParallel, noBenchmark
	matrixTargets = targets
	cosmoPlatforms = DefaultCosmoPlatforms
	outputDir = outDir
	releaseParallel = 1
	noBenchmark = true
	t.Cleanup(func() {
		matrixTargets, cosmoPlatforms = oldTargets, oldPlatforms
		outputDir, releaseParallel, noBenchmark = oldOutput, oldParallel, oldBench
	})
	return fakeGoroot, outDir
}

// A cosmo build produces a SINGLE file. This pins that outcome from the outside --
// the build directory itself -- rather than from any flag: a copy of the APE
// under a per-platform name is a thing this repo cannot express.
func TestRunReleaseWithRunnerCosmoTarget(t *testing.T) {
	stubVetPhase(t)
	fakeGoroot, outDir := setupCosmoMatrixTest(t, []string{"cosmo"})

	mock := newTestPassMock(0)
	origHandler := mock.Handler
	// The production spelling; NT adds .exe.
	cosmoGo := cosmoGoBinPath(fakeGoroot)
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.Name == cosmoGo && len(cfg.Args) > 0 && cfg.Args[0] == "build" {
			writeBuildOutput(t, cfg, "FAT-APE")
			return runner.MockProcess(nil, nil), nil
		}
		return origHandler(cfg)
	}

	err := runReleaseWithRunner(mock)
	require.NoError(t, err)

	// The APE is the only binary that exists as a file; the convenience names are symlinks to it.
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

	// The APE lands under the plain name, so the file IS the served filename.
	assert.Equal(t, name, manifest.Artifacts[0].File)
	assert.Equal(t, DefaultCosmoPlatforms, manifest.Artifacts[0].Platforms)

	// checksums.txt covers the real file alone.
	sums, err2 := os.ReadFile(filepath.Join(outDir, "checksums.txt"))
	assert.Nil(t, err2)
	sumLines := strings.Split(strings.TrimSpace(string(sums)), "\n")
	assert.Equal(t, 1, len(sumLines))
	assert.Contains(t, string(sums), fatName)

	// The cosmo build must run the gosmopolitan go with the fat-APE env.
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
		// Cache isolation: the env must carry the namespace derived from THIS toolchain's content.
		wantNS, nsErr := forkToolchainCacheNamespace(fakeGoroot)
		require.NoError(t, nsErr)
		require.NotEmpty(t, wantNS)
		ns, _ := cosmoCfg.Env.Get(cache.KeyNamespaceEnv)
		assert.Equal(t, wantNS, ns, "cosmo build env must set %s from the toolchain content hash", cache.KeyNamespaceEnv)
	}
}

func TestRunReleaseWithRunnerCosmoToolchainFailureFailsFast(t *testing.T) {
	stubVetPhase(t)
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
	stubVetPhase(t)
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
	t.Parallel()
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
			// The mocked fork build writes its -o output like the real compiler.
			mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
				writeBuildOutput(t, cfg, "BIN")
				return runner.MockProcess(nil, nil), nil
			}
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
	t.Parallel()
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

// The APE and the wasm targets are the only things this pipeline compiles, and
// both compile with the fork. runBuild is the sole place anything is compiled,
// so a job naming a native platform, or naming no toolchain, dies here — no
// call site can reintroduce a per-platform binary or another compiler.
func TestRunBuildRefusesAnythingButThePortableTargets(t *testing.T) {
	t.Parallel()
	forkGoroot := filepath.Join(t.TempDir(), "fork-goroot")
	for _, tc := range []struct {
		name    string
		job     buildJob
		wantErr string
	}{
		{
			name:    "native host platform",
			job:     buildJob{goos: "linux", goarch: "amd64", forkGoroot: forkGoroot, cacheNamespace: "ns"},
			wantErr: "has no build path",
		},
		{
			name:    "native cross-compile",
			job:     buildJob{goos: "darwin", goarch: "arm64", forkGoroot: forkGoroot, cacheNamespace: "ns"},
			wantErr: "has no build path",
		},
		{
			name:    "wasm GOOS without GOARCH=wasm",
			job:     buildJob{goos: "js", goarch: "amd64", forkGoroot: forkGoroot, cacheNamespace: "ns"},
			wantErr: "has no build path",
		},
		{
			name:    "the APE without the fork toolchain",
			job:     buildJob{goos: cosmoOS, goarch: cosmoFatArch, cacheNamespace: "ns"},
			wantErr: "no gosmopolitan GOROOT",
		},
		{
			name:    "an empty job, as a zero-value buildJob would be",
			job:     buildJob{},
			wantErr: "no gosmopolitan GOROOT",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := runner.NewMock()
			job := tc.job
			job.srcPath, job.outputPath = ".", filepath.Join(t.TempDir(), "out")
			err := runBuild(mock, job, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.Empty(t, mock.Calls(), "the compiler must not run at all")
			assert.NoFileExists(t, job.outputPath)
		})

	}
}

// The targets that DO build, through the same chokepoint.
func TestRunBuildAcceptsTheAPEAndWasm(t *testing.T) {
	t.Parallel()
	for _, p := range []buildPlatform{
		{OS: cosmoOS, Arch: cosmoFatArch},
		{OS: "js", Arch: wasmArch},
		{OS: "wasip1", Arch: wasmArch},
	} {
		t.Run(p.OS+"/"+p.Arch, func(t *testing.T) {
			mock := runner.NewMock()
			mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
				writeBuildOutput(t, cfg, "BIN")
				return runner.MockProcess(nil, nil), nil
			}
			job := buildJob{
				goos:           p.OS,
				goarch:         p.Arch,
				srcPath:        ".",
				outputPath:     filepath.Join(t.TempDir(), "out"),
				forkGoroot:     filepath.Join(t.TempDir(), "fork-goroot"),
				cacheNamespace: "deadbeef00c0ffee",
			}
			require.NoError(t, runBuild(mock, job, nil))
			require.Len(t, mock.Calls(), 1)
			assert.True(t, mock.Calls()[0].Env.Contains(cache.KeyNamespaceEnv))
		})
	}
}
