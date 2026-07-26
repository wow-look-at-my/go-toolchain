package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

func TestIsOutputArtifact(t *testing.T) {
	// Every shape the build phase, the matrix, and the slot copies write.
	for _, base := range []string{
		"mytool",
		"mytool.exe",
		"mytool_linux_amd64",
		"mytool_windows_amd64.exe",
		"mytool_cosmo_fat",
		"mytool_wasm_js",
		"mytool_js_wasm.wasm",
		"mytool_host",
		"mytool_host.exe",
	} {
		assert.True(t, isOutputArtifact(base, "mytool"), "%s is an artifact of mytool", base)
	}

	// Files belonging to something else, or to no target at all.
	for _, base := range []string{
		"othertool",
		"mytoolx",
		"mytool-notes.txt",
		"checksums.txt",
		"profile.json",
	} {
		assert.False(t, isOutputArtifact(base, "mytool"), "%s is not an artifact of mytool", base)
	}

	// A target name that prefixes a non-binary output must not consume it.
	assert.False(t, isOutputArtifact("wasm_exec.js", "wasm"))
	assert.True(t, isOutputArtifact("wasm_linux_amd64", "wasm"))

	// An empty target name matches nothing (a module with no resolvable name
	// must never sweep the whole output directory).
	assert.False(t, isOutputArtifact("mytool", ""))
	assert.False(t, isOutputArtifact("_host", ""))
}

// writeOutputDir creates dir with the given file names and returns dir.
func writeOutputDir(t *testing.T, dir string, names ...string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	for _, name := range names {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("binary"), 0o755))
	}
	return dir
}

func TestRemoveBuildOutputsIn(t *testing.T) {
	dir := writeOutputDir(t, filepath.Join(t.TempDir(), "build"),
		"mytool", "mytool_linux_amd64", "mytool_cosmo_fat", "checksums.txt", "unrelated")
	// A stale host symlink is unlinked like any other artifact — and following
	// it must never be required (its target is already gone).
	require.NoError(t, os.Symlink("mytool_linux_amd64", filepath.Join(dir, "mytool_host")))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "mytool_dir"), 0o755))

	removed, err := removeBuildOutputsIn(dir, []string{"mytool"})
	require.NoError(t, err)
	assert.Len(t, removed, 4, "removed: %v", removed)

	for _, gone := range []string{"mytool", "mytool_linux_amd64", "mytool_cosmo_fat", "mytool_host"} {
		assert.NoFileExists(t, filepath.Join(dir, gone))
	}
	assert.FileExists(t, filepath.Join(dir, "checksums.txt"))
	assert.FileExists(t, filepath.Join(dir, "unrelated"))
	assert.DirExists(t, filepath.Join(dir, "mytool_dir"), "directories are never artifacts")

	// Idempotent: a second pass removes nothing and does not error.
	removed, err = removeBuildOutputsIn(dir, []string{"mytool"})
	require.NoError(t, err)
	assert.Empty(t, removed)

	// A missing output directory is not an error — nothing was built yet.
	removed, err = removeBuildOutputsIn(filepath.Join(dir, "nope"), []string{"mytool"})
	require.NoError(t, err)
	assert.Empty(t, removed)
}

// setupOutputModule creates a temp module named mytool, chdirs into it, points
// outputDir at its build/ directory, and resets the tracking state.
func setupOutputModule(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmp))
	t.Cleanup(func() { os.Chdir(oldWd) })

	require.NoError(t, os.WriteFile("go.mod", []byte("module example.com/mytool\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile("main.go", []byte("package main\n\nfunc main() {}\n"), 0o644))

	oldOut := outputDir
	outputDir = "build"
	resetTrackedOutputs()
	t.Cleanup(func() {
		outputDir = oldOut
		resetTrackedOutputs()
	})
	// t.TempDir() on macOS is a /var symlink to /private/var; resolve it so
	// the tracked absolute paths compare equal to what the test writes.
	resolved, err := filepath.EvalSymlinks(tmp)
	require.NoError(t, err)
	return resolved
}

func TestClearBuildOutputsDeletesPreviousRunBinaries(t *testing.T) {
	tmp := setupOutputModule(t)
	dir := writeOutputDir(t, filepath.Join(tmp, "build"),
		"mytool", "mytool_linux_amd64", "checksums.txt")

	require.NoError(t, clearBuildOutputs(runner.New()))

	assert.NoFileExists(t, filepath.Join(dir, "mytool"),
		"the previous run's binary must not survive into this run")
	assert.NoFileExists(t, filepath.Join(dir, "mytool_linux_amd64"))
	assert.FileExists(t, filepath.Join(dir, "checksums.txt"))

	// The module is tracked, so a failure later in the pipeline can delete
	// whatever the build phase wrote in the meantime.
	tracked := trackedOutputsSnapshot()
	require.Len(t, tracked, 1)
	assert.Equal(t, dir, tracked[0].dir)
	assert.Equal(t, []string{"mytool"}, tracked[0].names)
}

func TestDiscardBuildOutputsRemovesBinariesBuiltThisRun(t *testing.T) {
	tmp := setupOutputModule(t)
	dir := filepath.Join(tmp, "build")

	// Clear with nothing built yet, as the pipeline does before its phases.
	require.NoError(t, clearBuildOutputs(runner.New()))

	// The build phase then succeeds and a LATER phase fails (a red dats suite,
	// the warnings gate): the freshly built binary must go too, or it stands
	// as the result of a run that failed.
	writeOutputDir(t, dir, "mytool", "checksums.txt")
	discardBuildOutputs()

	assert.NoFileExists(t, filepath.Join(dir, "mytool"))
	assert.FileExists(t, filepath.Join(dir, "checksums.txt"))
}

func TestDiscardBuildOutputsIsIndependentOfWorkingDirectory(t *testing.T) {
	tmp := setupOutputModule(t)
	dir := writeOutputDir(t, filepath.Join(tmp, "build"), "mytool")
	require.NoError(t, clearBuildOutputs(runner.New()))
	writeOutputDir(t, dir, "mytool")

	// A multi-module run clears each module from that module's directory and
	// can fail after chdir'ing elsewhere; the tracked paths are absolute.
	other := t.TempDir()
	require.NoError(t, os.Chdir(other))
	discardBuildOutputs()

	assert.NoFileExists(t, filepath.Join(dir, "mytool"))
}

func TestDiscardBuildOutputsFromCWD(t *testing.T) {
	tmp := setupOutputModule(t)
	dir := writeOutputDir(t, filepath.Join(tmp, "build"), "mytool", "mytool_host", "checksums.txt")

	// The guard/bootstrap exit paths never entered the pipeline, so nothing is
	// tracked — they resolve the module from the current directory instead.
	removed := discardBuildOutputsFromCWD()

	assert.ElementsMatch(t,
		[]string{filepath.Join(dir, "mytool"), filepath.Join(dir, "mytool_host")},
		removed)
	assert.NoFileExists(t, filepath.Join(dir, "mytool"))
	assert.FileExists(t, filepath.Join(dir, "checksums.txt"))
}

// setupPipelineOutputTest chdirs into a mock project (module "example.com"
// with a main package under pkg/, so the binary is named "example.com"),
// points outputDir at its build/ directory and resets the tracking state.
func setupPipelineOutputTest(t *testing.T) (buildDir, binary string) {
	t.Helper()
	tmp := t.TempDir()
	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmp))
	t.Cleanup(func() { os.Chdir(oldWd) })
	setupMockProject()

	oldOut, oldJSON := outputDir, jsonOutput
	outputDir, jsonOutput = "build", true
	resetTrackedOutputs()
	t.Cleanup(func() {
		outputDir, jsonOutput = oldOut, oldJSON
		resetTrackedOutputs()
	})
	return filepath.Join(tmp, "build"), "example.com"
}

func TestPipelineDeletesStaleBinaryWhenTestsFail(t *testing.T) {
	buildDir, binary := setupPipelineOutputTest(t)
	// A binary left by an earlier, successful run.
	writeOutputDir(t, buildDir, binary)

	err := runWithRunner(newTestFailWithErrorMock(), nil)

	require.Error(t, err, "failing tests must fail the run")
	assert.NoFileExists(t, filepath.Join(buildDir, binary),
		"a run whose tests failed must not leave the previous run's binary to be executed as its result")
}

func TestPipelineDeletesStaleBinaryWhenBuildFails(t *testing.T) {
	buildDir, binary := setupPipelineOutputTest(t)
	writeOutputDir(t, buildDir, binary)

	err := runWithRunner(newBuildFailMock(), nil)

	require.Error(t, err, "a failing compile must fail the run")
	assert.NoFileExists(t, filepath.Join(buildDir, binary))
}

func TestPipelineKeepsTheBinaryItJustBuilt(t *testing.T) {
	buildDir, binary := setupPipelineOutputTest(t)
	// The stale binary from the previous run is still deleted up front; what
	// must survive is the one this run's `go build` writes.
	writeOutputDir(t, buildDir, binary)

	mock := newTestPassMock(0)
	inner := mock.Handler
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "build") {
			writeBuildOutput(t, cfg, "FRESH")
			return runner.MockProcess(nil, nil), nil
		}
		return inner(cfg)
	}

	require.NoError(t, runWithRunner(mock, nil))

	body, err := os.ReadFile(filepath.Join(buildDir, binary))
	require.NoError(t, err, "a green run must leave its binary in place")
	assert.Equal(t, "FRESH", string(body), "the surviving binary must be the one this run built")
}

func TestDiscardBuildOutputsFromCWDWithoutModule(t *testing.T) {
	// No go.mod, no targets: silent no-op, never an error or a panic.
	tmp := t.TempDir()
	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmp))
	t.Cleanup(func() { os.Chdir(oldWd) })

	oldOut := outputDir
	outputDir = "build"
	t.Cleanup(func() { outputDir = oldOut })

	assert.Empty(t, discardBuildOutputsFromCWD())
}
