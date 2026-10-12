package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// pinGit runs git in dir with a fixed identity and no user config, and answers stdout.
func pinGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

// pinFixture is a submodule remote with a line of history and a side branch,
// and a superproject remote whose master pins the middle commit.
type pinFixture struct {
	super, work      string
	old, mid, latest string
	side             string
}

func newPinFixture(t *testing.T) pinFixture {
	root := t.TempDir()
	var f pinFixture

	subRemote := filepath.Join(root, "sub.git")
	pinGit(t, root, "init", "--quiet", "--bare", "-b", "master", subRemote)
	sub := filepath.Join(root, "sub")
	pinGit(t, root, "init", "--quiet", "-b", "master", sub)
	commit := func(msg string) string {
		pinGit(t, sub, "commit", "--quiet", "--allow-empty", "-m", msg)
		return pinGit(t, sub, "rev-parse", "HEAD")
	}
	f.old = commit("old")
	f.mid = commit("mid")
	f.latest = commit("latest")
	pinGit(t, sub, "checkout", "--quiet", "-b", "side", f.old)
	f.side = commit("side")
	pinGit(t, sub, "push", "--quiet", subRemote, "master", "side")

	f.super = filepath.Join(root, "super.git")
	pinGit(t, root, "init", "--quiet", "--bare", "-b", "master", f.super)
	f.work = filepath.Join(root, "work")
	pinGit(t, root, "init", "--quiet", "-b", "master", f.work)
	gitmodules := "[submodule \"dep\"]\n\tpath = dep\n\turl = file://" + filepath.ToSlash(subRemote) + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(f.work, ".gitmodules"), []byte(gitmodules), 0o644))
	pinGit(t, f.work, "add", ".gitmodules")
	pinGit(t, f.work, "update-index", "--add", "--cacheinfo", "160000,"+f.mid+",dep")
	pinGit(t, f.work, "commit", "--quiet", "-m", "pin mid")
	pinGit(t, f.work, "push", "--quiet", f.super, "master")
	return f
}

// branchPinning clones the superproject shallowly and commits a pin of dep on a branch.
func (f pinFixture) branchPinning(t *testing.T, commit string) string {
	clone := filepath.Join(t.TempDir(), "clone")
	pinGit(t, filepath.Dir(clone), "clone", "--quiet", "--depth=1", "file://"+filepath.ToSlash(f.super), clone)
	pinGit(t, clone, "checkout", "--quiet", "-b", "feature")
	pinGit(t, clone, "update-index", "--cacheinfo", "160000,"+commit+",dep")
	pinGit(t, clone, "commit", "--quiet", "--allow-empty", "-m", "pin")
	return clone
}

func TestSubmodulePinMayMoveForward(t *testing.T) {
	f := newPinFixture(t)
	assert.NoError(t, checkSubmodulesForwardIn(runner.New(), f.branchPinning(t, f.latest)))
}

func TestSubmodulePinMayStay(t *testing.T) {
	f := newPinFixture(t)
	assert.NoError(t, checkSubmodulesForwardIn(runner.New(), f.branchPinning(t, f.mid)))
}

func TestSubmodulePinMayNotMoveBack(t *testing.T) {
	f := newPinFixture(t)
	err := checkSubmodulesForwardIn(runner.New(), f.branchPinning(t, f.old))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dep: "+f.old+" is older than "+f.mid)
}

func TestSubmodulePinMayNotLeaveTheLine(t *testing.T) {
	f := newPinFixture(t)
	err := checkSubmodulesForwardIn(runner.New(), f.branchPinning(t, f.side))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dep: "+f.side+" does not descend from "+f.mid)
}

// The default branch moves after the clone, so its tree is not local and a scratch fetch reads it.
func TestSubmodulePinReadsADefaultBranchTheCloneLacks(t *testing.T) {
	f := newPinFixture(t)
	clone := f.branchPinning(t, f.old)
	pinGit(t, f.work, "commit", "--quiet", "--allow-empty", "-m", "later")
	pinGit(t, f.work, "push", "--quiet", f.super, "master")
	err := checkSubmodulesForwardIn(runner.New(), clone)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is older than "+f.mid)
}

func TestSubmodulePinWithNoOriginHasNothingToCompare(t *testing.T) {
	f := newPinFixture(t)
	assert.NoError(t, checkSubmodulesForwardIn(runner.New(), f.work))
}

func TestResolveSubmoduleURL(t *testing.T) {
	assert.Equal(t, "https://github.com/o/dep.git", resolveSubmoduleURL("../dep.git", "https://github.com/o/super.git"))
	assert.Equal(t, "https://github.com/o/super.git/dep", resolveSubmoduleURL("./dep", "https://github.com/o/super.git"))
	assert.Equal(t, "https://example.com/x.git", resolveSubmoduleURL("https://example.com/x.git", "https://github.com/o/super.git"))
}
