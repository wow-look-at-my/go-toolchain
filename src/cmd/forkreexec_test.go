package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
)

// The fork spells itself into its own version, and the test binary was built by
// whichever toolchain ran it. So this asserts the two answers agree rather than
// pinning either one.
func TestBuiltByForkReadsTheRunningToolchain(t *testing.T) {
	assert.Equal(t, strings.Contains(runtime.Version(), "cosmo"), builtByFork())
}

// A run inside another module has no pipeline source to rebuild from, whatever
// that module holds.
func TestAnotherModuleOffersNoSourceToRebuildFrom(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/other\n\ngo 1.27\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644))
	t.Chdir(dir)

	_, ok := ownMainPackage()
	assert.False(t, ok, "the module path decides, not the presence of a main package")
}

// A directory that is no module at all is the same answer by a different route.
func TestNoModuleOffersNoSourceToRebuildFrom(t *testing.T) {
	t.Chdir(t.TempDir())
	_, ok := ownMainPackage()
	assert.False(t, ok)
}

// notForkBuilt makes the gates below the fork check reachable. Every test binary
// here is fork-built, so without this the whole path is dead in the test run.
func notForkBuilt(t *testing.T) {
	t.Helper()
	prev := builtByForkFunc
	builtByForkFunc = func() bool { return false }
	t.Cleanup(func() { builtByForkFunc = prev })
}

// A fork-built pipeline is already the binary a rebuild would produce.
func TestAForkBuiltPipelineRebuildsNothing(t *testing.T) {
	t.Serial()
	prev := builtByForkFunc
	builtByForkFunc = func() bool { return true }
	t.Cleanup(func() { builtByForkFunc = prev })

	assert.NoError(t, reexecUnderFork())
}

// A rebuild that comes back still not fork-built must report that and carry on:
// rebuilding again produces the same binary.
func TestTheGuardStopsASecondRebuild(t *testing.T) {
	t.Serial()
	notForkBuilt(t)
	t.Setenv(reexecGuardEnv, "1")
	t.Chdir(t.TempDir())

	err := reexecUnderFork()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "still reports", "it names what came back, not just that something failed")
}

// Outside this module there is no source to rebuild from. The fork is the only
// compiler these phases run under, so there is no degraded mode to fall into:
// the run stops and names the repair.
func TestNoSourceToRebuildFromFailsTheRun(t *testing.T) {
	t.Serial()
	notForkBuilt(t)
	// A run of this pipeline re-execs, so its own tests inherit the guard from
	// the environment and would answer the guard's error instead.
	t.Setenv(reexecGuardEnv, "")
	t.Chdir(t.TempDir())

	err := reexecUnderFork()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the only compiler")
}

// The name the host needs on an executable. NT needs the suffix and a posix host
// does not care, so this reads the host rather than the compile target.
func TestTheExecutableSuffixFollowsTheHost(t *testing.T) {
	got := hostExeSuffix()
	if hostos.GOOS() == "windows" {
		assert.Equal(t, ".exe", got)
		return
	}
	assert.Empty(t, got)
}
