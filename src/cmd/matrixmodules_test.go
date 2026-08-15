package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// A repository root with no go.mod is a tree of modules. matrix has to walk it
// the way the default pipeline does, instead of tidying the root, finding no
// module, and dying on "no go.mod found".

// moduleTree writes one module per name into a fresh working directory. Each
// name in withMain gets a main package, so the module builds a binary; the
// rest are libraries.
func moduleTree(t *testing.T, names []string, withMain ...string) string {
	t.Helper()
	dir := t.TempDir()
	old, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { os.Chdir(old) })

	mains := map[string]bool{}
	for _, n := range withMain {
		mains[n] = true
	}
	for _, name := range names {
		require.NoError(t, os.MkdirAll(name, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(name, "go.mod"),
			[]byte("module example.com/"+name+"\n\ngo 1.21\n"), 0o600))
		body := "package " + name + "\n"
		if mains[name] {
			body = "package main\n\nfunc main() {}\n"
		}
		require.NoError(t, os.WriteFile(filepath.Join(name, name+".go"), []byte(body), 0o600))
	}
	return dir
}

// matrixTestFlags pins the target matrix and the output directory, and puts
// every package-level knob the run touches back afterwards.
func matrixTestFlags(t *testing.T) {
	t.Helper()
	oldOS, oldArch, oldOutput, oldParallel := matrixOS, matrixArch, outputDir, releaseParallel
	oldAllowed, oldBuilt := libraryModulesAllowed, matrixBuiltBinaries
	matrixOS = []string{"linux"}
	matrixArch = []string{"amd64"}
	outputDir = "dist"
	releaseParallel = 1
	t.Cleanup(func() {
		matrixOS, matrixArch, outputDir, releaseParallel = oldOS, oldArch, oldOutput, oldParallel
		libraryModulesAllowed, matrixBuiltBinaries = oldAllowed, oldBuilt
	})
}

func TestMatrixBuildsEveryModuleInTheTree(t *testing.T) {
	dir := moduleTree(t, []string{"cli", "tool"}, "cli", "tool")
	matrixTestFlags(t)

	require.NoError(t, runMatrixModules(newTestPassMock(0)))

	assert.FileExists(t, filepath.Join(dir, "cli", "dist", "cli_linux_amd64"))
	assert.FileExists(t, filepath.Join(dir, "tool", "dist", "tool_linux_amd64"))
}

// A library beside the module that ships the binary is the ordinary shape of
// such a tree. It has nothing to cross-compile, and that is not a failure.
func TestMatrixPassesOverALibraryModule(t *testing.T) {
	dir := moduleTree(t, []string{"cli", "reader"}, "cli")
	matrixTestFlags(t)

	require.NoError(t, runMatrixModules(newTestPassMock(0)))

	assert.FileExists(t, filepath.Join(dir, "cli", "dist", "cli_linux_amd64"))
	assert.NoFileExists(t, filepath.Join(dir, "reader", "dist", "reader_linux_amd64"))
}

// The command exists to produce binaries. A tree of nothing but libraries
// produced none, and says so rather than reporting a green run.
func TestMatrixFailsWhenNoModuleBuildsABinary(t *testing.T) {
	moduleTree(t, []string{"reader", "writer"})
	matrixTestFlags(t)

	err := runMatrixModules(newTestPassMock(0))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no main packages found to build in any of the 2 modules")
}

// One module in the tree, at the root: the single-module contract is
// unchanged, down to the error a library repo already got.
func TestMatrixKeepsTheSingleModuleContract(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { os.Chdir(old) })
	require.NoError(t, os.WriteFile("go.mod", []byte("module example.com/lib\n\ngo 1.21\n"), 0o600))
	require.NoError(t, os.WriteFile("lib.go", []byte("package lib\n"), 0o600))
	matrixTestFlags(t)

	err := runMatrixModules(newTestPassMock(0))

	require.Error(t, err)
	assert.Equal(t, "no main packages found to build", err.Error())
}

func TestMatrixReturnsToTheDirectoryItStartedIn(t *testing.T) {
	dir := moduleTree(t, []string{"cli"}, "cli")
	matrixTestFlags(t)

	require.NoError(t, runMatrixModules(newTestPassMock(0)))

	wd, err := os.Getwd()
	require.NoError(t, err)
	assert.Equal(t, resolvedPath(t, dir), resolvedPath(t, wd))
}

// A tree with no module at all still runs its suites, as the default pipeline
// does -- the CLI a suite drives does not have to be Go.
func TestMatrixWithoutAModuleFailsOnTheSameMessageAsTheDefaultPipeline(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { os.Chdir(old) })
	matrixTestFlags(t)

	err := runMatrixModules(runner.NewMock())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no go.mod and no dats/ suites found")
}

// resolvedPath resolves symlinks so a macOS /var vs /private/var TempDir does
// not read as a different directory.
func resolvedPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)
	return resolved
}
