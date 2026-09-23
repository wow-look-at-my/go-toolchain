package cmd

import (
	"encoding/json"
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

// Every build path resolves the go command, so any test reaching the build
// phase needs this. It answers the GOROOT.
func stubForkToolchain(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "fake-goroot")
	require.NoError(t, os.MkdirAll(root, 0o755))
	exe := filepath.Join(root, "go-toolchain")
	require.NoError(t, os.WriteFile(exe, []byte("fake go-toolchain"), 0o755))
	oldCmd, oldRoot := activeGoCmd, activeGoroot
	activeGoCmd = []string{exe, "go"}
	activeGoroot = root
	t.Cleanup(func() {
		activeGoCmd, activeGoroot = oldCmd, oldRoot
	})
	return root
}

// isForkBuild recognizes a build the stubbed go command runs.
func isForkBuild(cfg runner.Config, fakeGoroot string) bool {
	return cfg.Name == filepath.Join(fakeGoroot, "go-toolchain") && isGoBuild(cfg)
}

// setupCosmoMatrixTest points the matrix flags at the given targets, stubs
// the cosmo toolchain resolution, and restores everything on cleanup. It
// returns the fake GOROOT and the output directory.
func setupCosmoMatrixTest(t *testing.T, targets []string) (fakeGoroot, outDir string) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

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
	t.Serial()
	fakeGoroot, outDir := setupCosmoMatrixTest(t, []string{"cosmo"})

	mock := newTestPassMock(0)
	origHandler := mock.Handler
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if isForkBuild(cfg, fakeGoroot) {
			writeBuildOutput(t, cfg, fakeAPE)
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

	// The cosmo build must run this binary's go command with the fat-APE env.
	var cosmoCfg *runner.Config
	for _, cfg := range mock.Calls() {
		if isForkBuild(cfg, fakeGoroot) {
			c := cfg
			cosmoCfg = &c
		}
	}
	if assert.NotNil(t, cosmoCfg, "expected a build via this binary's go command") {
		assert.Equal(t, "go", cosmoCfg.Args[0], "the go command is this binary under its go subcommand")
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
		cgo, _ := cosmoCfg.Env.Get("CGO_ENABLED")
		assert.Equal(t, "0", cgo)
	}
}

// With no go command set up there is nothing to build with, and the run says
// which call was skipped rather than reaching the test phase.
func TestRunReleaseWithRunnerWithoutAGoCommandFailsFast(t *testing.T) {
	t.Serial()
	setupCosmoMatrixTest(t, []string{"cosmo"})
	activeGoCmd = nil

	mock := newTestPassMock(0)
	err := runReleaseWithRunner(mock)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "EnsureGoVersion")
	// Fail-fast: the toolchain is resolved before the test phase runs.
	for _, cfg := range mock.Calls() {
		assert.False(t, cfg.IsCmd("go", "test"), "tests must not run without a go command")
	}
}

func TestRunReleaseWithRunnerInvalidTargets(t *testing.T) {
	t.Serial()
	oldTargets := matrixTargets
	matrixTargets = []string{"cosmo/amd64"}
	defer func() { matrixTargets = oldTargets }()

	mock := newTestPassMock(0)
	err := runReleaseWithRunner(mock)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "fat APE")
}

// The APE and the wasm targets are the only things this pipeline compiles, and
// both compile with the fork. runBuild is the sole place anything is compiled,
// so a job naming a native platform, or naming no toolchain, dies here — no
// call site can reintroduce a per-platform binary or another compiler.
func TestRunBuildRefusesAnythingButThePortableTargets(t *testing.T) {
	t.Serial()
	goCmd := []string{filepath.Join(t.TempDir(), "go-toolchain"), "go"}
	for _, tc := range []struct {
		name    string
		job     buildJob
		wantErr string
	}{
		{
			name:    "native host platform",
			job:     buildJob{goos: "linux", goarch: "amd64", goCmd: goCmd},
			wantErr: "has no build path",
		},
		{
			name:    "native cross-compile",
			job:     buildJob{goos: "darwin", goarch: "arm64", goCmd: goCmd},
			wantErr: "has no build path",
		},
		{
			name:    "wasm GOOS without GOARCH=wasm",
			job:     buildJob{goos: "js", goarch: "amd64", goCmd: goCmd},
			wantErr: "has no build path",
		},
		{
			name:    "the APE without a go command",
			job:     buildJob{goos: cosmoOS, goarch: cosmoFatArch},
			wantErr: "names no go command",
		},
		{
			name:    "an empty job, as a zero-value buildJob would be",
			job:     buildJob{},
			wantErr: "names no go command",
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
	t.Serial()
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
				goos:       p.OS,
				goarch:     p.Arch,
				srcPath:    ".",
				outputPath: filepath.Join(t.TempDir(), "out"),
				goCmd:      []string{filepath.Join(t.TempDir(), "go-toolchain"), "go"},
				goroot:     filepath.Join(t.TempDir(), "fork-goroot"),
			}
			require.NoError(t, runBuild(mock, job, nil))
			require.Len(t, mock.Calls(), 1)
			assert.True(t, mock.Calls()[0].Env.Contains("GOROOT"))
		})
	}
}
