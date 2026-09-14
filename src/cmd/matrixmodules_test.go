package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// A repository root with no go.mod is a tree of modules. matrix has to walk it
// the way the default pipeline does, instead of tidying the root, finding no
// module, and dying on "no go.mod found".

// moduleTree writes one module per name into a fresh working directory. Each
// name in withMain gets a main package, so the module builds a binary; the
// rest are libraries.
func moduleTree(t *testing.T, names []string, withMain ...string) {
	t.Helper()
	dir := t.TempDir()
	old, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { os.Chdir(old) })

	mains := set.Of(withMain...)
	for _, name := range names {
		require.NoError(t, os.MkdirAll(name, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(name, "go.mod"),
			[]byte("module example.com/"+name+"\n\ngo 1.21\n"), 0o600))
		body := "package " + name + "\n"
		if mains.Contains(name) {
			body = "package main\n\nfunc main() {}\n"
		}
		require.NoError(t, os.WriteFile(filepath.Join(name, name+".go"), []byte(body), 0o600))
	}
}

// matrixTestFlags pins one explicit target and the output directory, and puts
// every package-level knob the run touches back afterwards. An explicit target
// discovers main packages under its own build context, so a library module
// resolves to nothing to build -- which is the case these tests are about.
func matrixTestFlags(t *testing.T) {
	t.Helper()
	oldTargets, oldOutput, oldParallel := matrixTargets, outputDir, releaseParallel
	oldAllowed, oldBuilt := libraryModulesAllowed, matrixBuiltBinaries
	matrixTargets = []string{"linux/amd64"}
	outputDir = "dist"
	releaseParallel = 1
	t.Cleanup(func() {
		matrixTargets, outputDir, releaseParallel = oldTargets, oldOutput, oldParallel
		libraryModulesAllowed, matrixBuiltBinaries = oldAllowed, oldBuilt
	})
}

// builtBinaries reports the -o path of every build the run asked for, so a
// test can say which modules produced a binary without a real compiler.
func builtBinaries(mock *runner.Mock) []string {
	var built []string
	for _, call := range mock.Calls() {
		if !call.IsCmd("go", "build") {
			continue
		}
		for i, arg := range call.Args {
			if arg == "-o" && i+1 < len(call.Args) {
				built = append(built, filepath.ToSlash(call.Args[i+1]))
			}
		}
	}
	return built
}

func TestMatrixBuildsEveryModuleInTheTree(t *testing.T) {
	moduleTree(t, []string{"cli", "tool"}, "cli", "tool")
	matrixTestFlags(t)
	mock := newTestPassMock(0)

	require.NoError(t, runMatrixModules(mock))

	assert.ElementsMatch(t, []string{"dist/cli_linux_amd64", "dist/tool_linux_amd64"}, builtBinaries(mock))
	assert.Equal(t, 2, matrixBuiltBinaries)
}

// A library beside the module that ships the binary is the ordinary shape of
// such a tree. It has nothing to cross-compile, and that is not a failure.
func TestMatrixPassesOverALibraryModule(t *testing.T) {
	moduleTree(t, []string{"cli", "reader"}, "cli")
	matrixTestFlags(t)
	mock := newTestPassMock(0)

	require.NoError(t, runMatrixModules(mock))

	assert.Equal(t, []string{"dist/cli_linux_amd64"}, builtBinaries(mock))
}

// The library module is still gated: only its build had nothing to do.
func TestMatrixStillTestsALibraryModule(t *testing.T) {
	moduleTree(t, []string{"cli", "reader"}, "cli")
	matrixTestFlags(t)
	mock := newTestPassMock(0)

	require.NoError(t, runMatrixModules(mock))

	var tested int
	for _, call := range mock.Calls() {
		if call.IsCmd("go", "test") {
			tested++
		}
	}
	assert.Equal(t, 2, tested, "both modules run their tests")
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

// One module, at the root: the single-module contract is unchanged, down to
// the error a library repo already got.
func TestMatrixKeepsTheSingleModuleContract(t *testing.T) {
	old, _ := os.Getwd()
	require.NoError(t, os.Chdir(t.TempDir()))
	t.Cleanup(func() { os.Chdir(old) })
	require.NoError(t, os.WriteFile("go.mod", []byte("module example.com/lib\n\ngo 1.21\n"), 0o600))
	require.NoError(t, os.WriteFile("lib.go", []byte("package lib\n"), 0o600))
	matrixTestFlags(t)

	err := runMatrixModules(newTestPassMock(0))

	require.Error(t, err)
	assert.Equal(t, "no main packages found to build", err.Error())
}

func TestMatrixReturnsToTheDirectoryItStartedIn(t *testing.T) {
	moduleTree(t, []string{"cli"}, "cli")
	matrixTestFlags(t)
	before, err := os.Getwd()
	require.NoError(t, err)

	require.NoError(t, runMatrixModules(newTestPassMock(0)))

	after, err := os.Getwd()
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

// Nothing to build and no suites to run: the message names both, as the
// default pipeline's does, rather than blaming a missing go.mod alone.
func TestMatrixWithoutAModuleOrSuitesNamesBoth(t *testing.T) {
	old, _ := os.Getwd()
	require.NoError(t, os.Chdir(t.TempDir()))
	t.Cleanup(func() { os.Chdir(old) })
	matrixTestFlags(t)

	err := runMatrixModules(runner.NewMock())

	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "no go.mod and no dats/ suites found"), err.Error())
}
