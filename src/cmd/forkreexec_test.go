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

func TestTheLinkedVersionIsTheRunningToolchain(t *testing.T) {
	assert.Equal(t, strings.Fields(runtime.Version())[0], linkedGoVersion())
}

// An experiment list follows the version after a space, and names no other toolchain.
func TestAnExperimentListIsNotPartOfTheVersion(t *testing.T) {
	assert.Equal(t, "go1.27.0-cosmo.r1293", goVersionName("go1.27.0-cosmo.r1293 X:nocoverageredesign"))
	assert.Equal(t, "go1.27.0-cosmo.r1293", goVersionName("go1.27.0-cosmo.r1293"))
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

// The published v852 pipeline links the front end of r1200-era fork source.
// Under r1293 it read runtime/goos_cosmo.go and stopped at `readonly var`.
const (
	olderFork  = "go1.27.0-cosmo.r1200"
	activeFork = "go1.27.0-cosmo.r1293"
)

// linkedTo makes this binary report the toolchain named. go test builds with the
// active toolchain, so without this every gate below it is dead in the test run.
func linkedTo(t *testing.T, version string) {
	t.Helper()
	prev := linkedGoVersionFunc
	linkedGoVersionFunc = func() string { return version }
	t.Cleanup(func() { linkedGoVersionFunc = prev })
}

// A pipeline the active toolchain built already parses what that toolchain accepts.
func TestAPipelineTheActiveToolchainBuiltRebuildsNothing(t *testing.T) {
	t.Serial()
	linkedTo(t, activeFork)

	assert.NoError(t, reexecUnderActiveToolchain(activeFork))
}

// Another release of the fork is another front end. Its go/parser and go/types
// do not know the syntax the active release added, so the pipeline is rebuilt
// even though a fork built it. Outside this module the rebuild fetches the
// binary's own commit, and a binary stamped with none stops there, which shows
// the rebuild was attempted.
func TestAPipelineLinkingAnotherForkReleaseIsRebuilt(t *testing.T) {
	t.Serial()
	linkedTo(t, olderFork)
	// A run re-execs, so its own tests inherit the guard from the environment.
	t.Setenv(reexecGuardEnv, "")
	t.Chdir(t.TempDir())
	prev := cachedVCS
	cachedVCS = &vcsInfo{}
	t.Cleanup(func() { cachedVCS = prev })

	err := reexecUnderActiveToolchain(activeFork)
	require.Error(t, err, "a fork-built pipeline of another release must not vet with its own front end")
	assert.Contains(t, err.Error(), "no commit to rebuild from")
	assert.Contains(t, err.Error(), olderFork)
	assert.Contains(t, err.Error(), activeFork)
}

// The rebuild's own child still linking another front end is a toolchain on
// PATH that did not build it. Rebuilding again produces the same binary.
func TestTheGuardStopsASecondRebuild(t *testing.T) {
	t.Serial()
	linkedTo(t, olderFork)
	t.Setenv(reexecGuardEnv, "1")
	t.Chdir(t.TempDir())

	err := reexecUnderActiveToolchain(activeFork)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not build it", "it names what came back, not just that something failed")
}

// A binary with no commit stamped on it cannot be fetched again, so the run
// stops and names the repair.
func TestAPipelineWithNoCommitFailsTheRun(t *testing.T) {
	_, err := pipelineAtRevision(olderFork, activeFork, vcsInfo{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no commit")
	assert.Contains(t, err.Error(), activeFork)
}

// A modified tree is a commit plus changes no clone carries.
func TestAPipelineFromAModifiedTreeFailsTheRun(t *testing.T) {
	_, err := pipelineAtRevision(olderFork, activeFork, vcsInfo{Revision: "0123456789abcdef", Modified: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "modified tree")
	assert.Contains(t, err.Error(), "0123456789abcdef")
}

// A build already cached for this toolchain and commit is used as it stands.
func TestACachedRebuildIsReused(t *testing.T) {
	t.Serial()
	root := t.TempDir()
	prev := goCacheDirFunc
	goCacheDirFunc = func() (string, error) { return root, nil }
	t.Cleanup(func() { goCacheDirFunc = prev })
	rev := "0123456789abcdef"
	want := filepath.Join(root, "pipeline", pipelineCacheKey(activeFork, rev), "go-toolchain"+hostExeSuffix())
	require.NoError(t, os.MkdirAll(filepath.Dir(want), 0o755))
	require.NoError(t, os.WriteFile(want, []byte("cached"), 0o755))

	got, err := pipelineAtRevision(olderFork, activeFork, vcsInfo{Revision: rev})
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// The key keeps every character a path accepts on every host, and names the toolchain and the commit.
func TestTheCacheKeyNamesTheToolchainAndTheCommit(t *testing.T) {
	assert.Equal(t, "go1.27.0-cosmo.r1293-abc123", pipelineCacheKey(activeFork, "abc123"))
	assert.Equal(t, "go1.27_devel_x-abc123", pipelineCacheKey("go1.27 devel:x", "abc123"))
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
