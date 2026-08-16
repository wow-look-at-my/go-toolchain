package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/build"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

func TestApeManifestEntries(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mytool_cosmo_fat"), []byte("APE"), 0755))
	targets := []build.Target{{ImportPath: "./cmd/mytool", OutputName: "mytool"}}

	entries, err := apeManifestEntries(targets, dir, []buildPlatform{
		{OS: "linux", Arch: "amd64"},
		{OS: "darwin", Arch: "arm64"},
	})
	require.NoError(t, err)
	assert.Equal(t, []buildhostManifestEntry{{
		File:      "mytool_cosmo_fat",
		Platforms: []string{"linux/amd64", "darwin/arm64"},
		// The download is served as the plain binary name, not the on-disk
		// _cosmo_fat one.
		Filename: "mytool",
	}}, entries)
}

func TestApeManifestEntriesRefusesUntrueManifest(t *testing.T) {
	dir := t.TempDir()
	targets := []build.Target{{ImportPath: "./cmd/mytool", OutputName: "mytool"}}

	// A manifest naming a file that is not there fails the publish; catching
	// it here says which artifact is missing instead.
	_, err := apeManifestEntries(targets, dir, []buildPlatform{{OS: "linux", Arch: "amd64"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mytool_cosmo_fat")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "mytool_cosmo_fat"), []byte("APE"), 0755))
	_, err = apeManifestEntries(targets, dir, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty platform set")
}

// The manifest is a wire contract with buildhost-publish: schema 1, and only
// the fields buildhost reads. kind is deliberately absent (it selects
// repackaging and defaults to binary; APE-ness is detected from the bytes).
func TestWriteBuildhostManifestShape(t *testing.T) {
	dir := t.TempDir()
	path, err := writeBuildhostManifest(dir, []buildhostManifestEntry{{
		File:      "mytool_cosmo_fat",
		Platforms: []string{"linux/amd64", "darwin/arm64", "windows/amd64"},
		Filename:  "mytool",
	}})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "buildhost-artifacts.json"), path)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), `"kind"`, "kind must be omitted so buildhost applies its own default")

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, float64(1), got["schema"])
	artifacts, ok := got["artifacts"].([]any)
	require.True(t, ok)
	require.Len(t, artifacts, 1)
	entry := artifacts[0].(map[string]any)
	assert.Equal(t, "mytool_cosmo_fat", entry["file"])
	assert.Equal(t, "mytool", entry["filename"])
	assert.Equal(t, []any{"linux/amd64", "darwin/arm64", "windows/amd64"}, entry["platforms"])
}

// The manifest describes the artifacts, so it must not outlive them: one left
// behind would send the next publish after a file that is gone.
func TestManifestIsClearedWithBuildOutputs(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, buildhostManifestName), []byte("{}"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mytool_cosmo_fat"), []byte("APE"), 0755))

	removed, err := removeBuildOutputsIn(dir, []string{"mytool"})
	require.NoError(t, err)
	assert.Len(t, removed, 2)
	assert.NoFileExists(t, filepath.Join(dir, buildhostManifestName))
}

// End to end on the default path: one APE, one manifest, no per-platform
// copies, and GOCOSMOPLATFORMS carrying the requested set.
func TestDefaultMatrixBuildsOneMultiPlatformArtifact(t *testing.T) {
	fakeGoroot, outDir := setupCosmoMatrixTest(t, nil)
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

	require.NoError(t, runReleaseWithRunner(mock))

	// Exactly one binary. Anything matching <name>_<os>_<arch> would be a
	// duplicate copy of it.
	entries, err := os.ReadDir(outDir)
	require.NoError(t, err)
	var binaries []string
	for _, e := range entries {
		if !e.Type().IsRegular() || e.Name() == "checksums.txt" || e.Name() == buildhostManifestName {
			continue
		}
		binaries = append(binaries, e.Name())
	}
	assert.Equal(t, []string{"mytool_cosmo_fat"}, binaries)

	// checksums.txt lists the APE once, under its real filename.
	sums, err := os.ReadFile(filepath.Join(outDir, "checksums.txt"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(sums)), "\n")
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "mytool_cosmo_fat")

	raw, err := os.ReadFile(filepath.Join(outDir, buildhostManifestName))
	require.NoError(t, err)
	var m buildhostManifest
	require.NoError(t, json.Unmarshal(raw, &m))
	assert.Equal(t, buildhostManifest{Schema: 1, Artifacts: []buildhostManifestEntry{{
		File:      "mytool_cosmo_fat",
		Platforms: []string{"linux/amd64", "darwin/arm64", "windows/amd64"},
		Filename:  "mytool",
	}}}, m)

	var cosmoCfg *runner.Config
	for _, cfg := range mock.Calls() {
		if cfg.Name == cosmoGo {
			c := cfg
			cosmoCfg = &c
		}
	}
	require.NotNil(t, cosmoCfg)
	platforms, _ := cosmoCfg.Env.Get(cosmoPlatformsEnv)
	assert.Equal(t, "linux/amd64,darwin/arm64,windows/amd64", platforms)
}

// --cosmo-platforms all asks for every payload the fork emits, so the variable
// is left unset (the fork's own default) rather than spelled out.
func TestCosmoPlatformsAllLeavesEnvUnset(t *testing.T) {
	fakeGoroot, outDir := setupCosmoMatrixTest(t, nil)
	t.Setenv("CI", "")
	cosmoPlatforms = []string{"all"}

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
	require.NoError(t, runReleaseWithRunner(mock))

	var cosmoCfg *runner.Config
	for _, cfg := range mock.Calls() {
		if cfg.Name == cosmoGo {
			c := cfg
			cosmoCfg = &c
		}
	}
	require.NotNil(t, cosmoCfg)
	platforms, _ := cosmoCfg.Env.Get(cosmoPlatformsEnv)
	assert.Equal(t, "", platforms)

	// The manifest still states where the binary runs, using the coverage a
	// full APE actually has.
	raw, err := os.ReadFile(filepath.Join(outDir, buildhostManifestName))
	require.NoError(t, err)
	var m buildhostManifest
	require.NoError(t, json.Unmarshal(raw, &m))
	assert.Equal(t, []string{"darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64"}, m.Artifacts[0].Platforms)
}

// With no native host binary — the default now — the dats phase and the
// convenience symlinks must fall back to the APE, which runs here.
func TestHostRunnableArtifactFallsBackToTheAPE(t *testing.T) {
	dir := t.TempDir()
	target := build.Target{ImportPath: "./cmd/mytool", OutputName: "mytool"}

	ape := filepath.Join(dir, "mytool_cosmo_fat")
	require.NoError(t, os.WriteFile(ape, []byte("APE"), 0755))
	assert.Equal(t, ape, hostRunnableArtifact(target, dir))

	native := filepath.Join(dir, build.BinaryName("mytool", hostos.GOOS(), runtime.GOARCH))
	require.NoError(t, os.WriteFile(native, []byte("NATIVE"), 0755))
	assert.Equal(t, native, hostRunnableArtifact(target, dir),
		"a real native build wins over the APE")

	// Neither present: the native path is returned so the caller reports the
	// artifact it actually wanted.
	require.NoError(t, os.Remove(ape))
	require.NoError(t, os.Remove(native))
	assert.Equal(t, native, hostRunnableArtifact(target, dir))
}
