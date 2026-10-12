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

// gitInFork runs git in dir and answers its trimmed stdout.
func gitInFork(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "protocol.file.allow=always"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

// forkWithOrgSubmodule builds a fork checkout in the working directory. Its org
// submodule follows master and stands at the newer of commits. It answers the
// older commit and the newer one.
func forkWithOrgSubmodule(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	dep := filepath.Join(root, "dep")
	require.NoError(t, os.MkdirAll(dep, 0o755))
	gitInFork(t, dep, "init", "--quiet", "--initial-branch", "master")
	gitInFork(t, dep, "commit", "--quiet", "--allow-empty", "-m", "older")
	older := gitInFork(t, dep, "rev-parse", "HEAD")
	gitInFork(t, dep, "commit", "--quiet", "--allow-empty", "-m", "newer")
	newer := gitInFork(t, dep, "rev-parse", "HEAD")

	t.Chdir(root)
	require.NoError(t, os.MkdirAll(forkSubmoduleDir, 0o755))
	gitInFork(t, forkSubmoduleDir, "init", "--quiet")
	gitmodules := "[submodule \"ipc\"]\n\tpath = src/vendor/ipc\n\turl = https://github.com/wow-look-at-my/go-ipc.git\n" +
		"[submodule \"stock\"]\n\tpath = src/vendor/stock\n\turl = https://github.com/golang/mod.git\n"
	require.NoError(t, os.WriteFile(filepath.Join(forkSubmoduleDir, ".gitmodules"), []byte(gitmodules), 0o644))
	gitInFork(t, forkSubmoduleDir, "config", "submodule.ipc.branch", "master")
	gitInFork(t, forkSubmoduleDir, "config", "submodule.stock.branch", "master")
	gitInFork(t, ".", "clone", "--quiet", "file://"+dep, filepath.Join(forkSubmoduleDir, "src", "vendor", "ipc"))
	return older, newer
}

// A commit that lands on the branch after the run locked it reaches no later job.
func TestLockForkSubmodulesTakesTheCommitTheRunLocked(t *testing.T) {
	t.Serial()
	older, newer := forkWithOrgSubmodule(t)
	locked := map[string]string{"commit:github.com/wow-look-at-my/go-ipc@master": older}
	fakeRunLockStore(t, locked)

	require.NoError(t, lockForkSubmodules(runner.New()))

	assert.Equal(t, older, gitInFork(t, filepath.Join(forkSubmoduleDir, "src", "vendor", "ipc"), "rev-parse", "HEAD"))
	assert.NotEqual(t, older, newer)
	assert.Len(t, locked, 1, "a submodule outside the org follows no branch head, so nothing locks it")
}

// The first job of the run locks the head it checked out.
func TestLockForkSubmodulesClaimsTheHead(t *testing.T) {
	t.Serial()
	_, newer := forkWithOrgSubmodule(t)
	locked := map[string]string{}
	fakeRunLockStore(t, locked)

	require.NoError(t, lockForkSubmodules(runner.New()))

	assert.Equal(t, newer, locked["commit:github.com/wow-look-at-my/go-ipc@master"])
	assert.Equal(t, newer, gitInFork(t, filepath.Join(forkSubmoduleDir, "src", "vendor", "ipc"), "rev-parse", "HEAD"))
}

// A store that cannot answer fails the build. Nothing stands in for the lock.
func TestLockForkSubmodulesFailsWithoutTheStore(t *testing.T) {
	t.Serial()
	forkWithOrgSubmodule(t)
	srv := fakeRunLockStore(t, map[string]string{})
	srv.Close()

	err := lockForkSubmodules(runner.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "commit:github.com/wow-look-at-my/go-ipc@master")
}

func TestOrgModuleOfURL(t *testing.T) {
	got, ok := orgModuleOfURL("https://github.com/wow-look-at-my/go-shm.git")
	assert.True(t, ok)
	assert.Equal(t, "github.com/wow-look-at-my/go-shm", got)
	_, ok = orgModuleOfURL("https://github.com/golang/mod.git")
	assert.False(t, ok)
}
