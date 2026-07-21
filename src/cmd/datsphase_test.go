package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

func TestHasDatsSuites(t *testing.T) {
	write := func(t *testing.T, dir, rel string) {
		t.Helper()
		path := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("tests: []\n"), 0o644))
	}

	t.Run("no dats dir", func(t *testing.T) {
		assert.False(t, hasDatsSuites(t.TempDir()))
	})

	t.Run("dats is a file, not a dir", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "dats"), nil, 0o644))
		assert.False(t, hasDatsSuites(dir))
	})

	t.Run("empty dats dir", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "dats"), 0o755))
		assert.False(t, hasDatsSuites(dir))
	})

	t.Run("non-dats files only", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "dats/README.md")
		write(t, dir, "dats/cli.dats.snapshots/001-x.stdout.golden")
		assert.False(t, hasDatsSuites(dir))
	})

	t.Run("suite at top level", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "dats/cli.dats")
		assert.True(t, hasDatsSuites(dir))
	})

	t.Run("suite in subdirectory", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "dats/sub/more.dats")
		assert.True(t, hasDatsSuites(dir))
	})

	t.Run("hidden suite file skipped", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "dats/.hidden.dats")
		assert.False(t, hasDatsSuites(dir))
	})

	t.Run("hidden dir skipped", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "dats/.git/x.dats")
		assert.False(t, hasDatsSuites(dir))
	})

	t.Run("nested module still counts", func(t *testing.T) {
		// dats' own discovery does not skip nested Go modules, so the gate
		// must not either — otherwise a suite dats would run could be
		// silently no-opped.
		dir := t.TempDir()
		write(t, dir, "dats/fixturemod/go.mod")
		write(t, dir, "dats/fixturemod/x.dats")
		assert.True(t, hasDatsSuites(dir))
	})
}

func TestDatsArtifactName(t *testing.T) {
	assert.Equal(t, "mytool", datsArtifactName("mytool", "linux"))
	assert.Equal(t, "mytool", datsArtifactName("mytool", "darwin"))
	assert.Equal(t, "mytool.exe", datsArtifactName("mytool", "windows"))
}

func TestStageDatsArtifacts(t *testing.T) {
	src := t.TempDir()
	real := filepath.Join(src, "mytool_linux_amd64")
	require.NoError(t, os.WriteFile(real, []byte("binary bytes"), 0o644))

	dir, cleanup, err := stageDatsArtifacts([]datsArtifact{
		{sourcePath: real, name: "mytool"},
		{sourcePath: filepath.Join(src, "missing_darwin_arm64"), name: "missing"},
	})
	require.NoError(t, err)
	defer cleanup()

	// The present artifact is copied under its bare name, executable.
	staged := filepath.Join(dir, "mytool")
	data, err := os.ReadFile(staged)
	require.NoError(t, err)
	assert.Equal(t, []byte("binary bytes"), data)
	info, err := os.Stat(staged)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&0o111, "staged artifact must be executable")

	// The missing artifact is skipped without failing the staging.
	assert.NoFileExists(t, filepath.Join(dir, "missing"))

	cleanup()
	assert.NoDirExists(t, dir)
}

// swapEnsureDats replaces the dats bootstrap seam for one test.
func swapEnsureDats(t *testing.T, fn func() (string, error)) {
	t.Helper()
	old := ensureDatsFunc
	ensureDatsFunc = fn
	t.Cleanup(func() { ensureDatsFunc = old })
}

func TestRunDatsPhaseNoSuitesIsNoOp(t *testing.T) {
	t.Chdir(t.TempDir())
	swapEnsureDats(t, func() (string, error) {
		t.Fatal("ensureDatsFunc must not be called when no suites exist")
		return "", nil
	})

	mock := runner.NewMock()
	require.NoError(t, runDatsPhase(mock, false, nil))
	assert.Empty(t, mock.Calls(), "no dats process may be spawned without suites")
}

func TestRunDatsPhaseRunsSuites(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "dats"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dats", "cli.dats"), []byte("tests: []\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "build"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "build", "mytool"), []byte("bin"), 0o755))
	t.Chdir(dir)
	swapEnsureDats(t, func() (string, error) { return "/fake/dats", nil })

	mock := runner.NewMock()
	// Inspect the staged handoff dir DURING the call — it is removed once the
	// phase returns.
	var stagedBinary string
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if buildDir, ok := cfg.Env.Get(datsBuildDirEnv); ok {
			stagedBinary = filepath.Join(buildDir, "mytool")
			if _, err := os.Stat(stagedBinary); err != nil {
				t.Errorf("staged artifact missing during dats run: %v", err)
			}
		}
		return nil, nil // fall through to the default empty-success response
	}

	artifacts := []datsArtifact{{sourcePath: filepath.Join(dir, "build", "mytool"), name: "mytool"}}
	require.NoError(t, runDatsPhase(mock, false, artifacts))

	calls := mock.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "/fake/dats", calls[0].Name)
	assert.Equal(t, []string{"test", "dats"}, calls[0].Args)

	buildDir, ok := calls[0].Env.Get(datsBuildDirEnv)
	require.True(t, ok, "dats must receive %s", datsBuildDirEnv)
	assert.NotEmpty(t, buildDir)
	assert.NoDirExists(t, buildDir, "handoff dir must be cleaned up after the run")
	require.NotEmpty(t, stagedBinary)

	// The cacheprog plumbing must be cleared for suite commands.
	v, ok := calls[0].Env.Get("GOCACHEPROG")
	require.True(t, ok)
	assert.Empty(t, v)
	v, ok = calls[0].Env.Get("GOCACHE_STATS_SOCK")
	require.True(t, ok)
	assert.Empty(t, v)
}

func TestRunDatsPhaseFailureFailsBuild(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "dats"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dats", "cli.dats"), []byte("tests: []\n"), 0o644))
	t.Chdir(dir)
	swapEnsureDats(t, func() (string, error) { return "/fake/dats", nil })

	mock := runner.NewMock()
	mock.SetResponse("/fake/dats", []string{"test", "dats"}, nil, fmt.Errorf("exit status 1"))

	err := runDatsPhase(mock, false, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dats suites failed")
}

func TestRunDatsPhaseBootstrapFailureFailsBuild(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "dats"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dats", "cli.dats"), []byte("tests: []\n"), 0o644))
	t.Chdir(dir)
	swapEnsureDats(t, func() (string, error) { return "", fmt.Errorf("download blew up") })

	mock := runner.NewMock()
	err := runDatsPhase(mock, false, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dats suites failed")
	assert.Contains(t, err.Error(), "download blew up")
	assert.Empty(t, mock.Calls())
}

func TestRunDatsPhaseQuietRoutesStdoutToStderr(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "dats"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dats", "cli.dats"), []byte("tests: []\n"), 0o644))
	t.Chdir(dir)
	swapEnsureDats(t, func() (string, error) { return "/fake/dats", nil })

	mock := runner.NewMock()
	require.NoError(t, runDatsPhase(mock, true, nil))

	calls := mock.Calls()
	require.Len(t, calls, 1)
	// --json mode: stdout carries the JSON payload, so the dats report must
	// be routed to stderr instead.
	assert.Equal(t, os.Stderr, calls[0].StdoutWriter)
}
