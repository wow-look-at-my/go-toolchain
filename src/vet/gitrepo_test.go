package vet

// Helpers for tests that need a real git repository (the uncommitted-changes
// guard tests here and in canonicalize_integration_test.go).

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// hermeticGitEnv returns an environment for child git processes that ignores
// the host's global and system config by pointing both at an empty file (not
// /dev/null — portable to Windows contributors). Without this the developer's
// config leaks into test repos: e.g. feature.manyFiles/index.skipHash (git >=
// 2.40) write a zero-hash index trailer go-git v5 cannot read, and
// init.defaultBranch, gpg signing, hooks, or fsmonitor could equally interfere.
func hermeticGitEnv(t *testing.T) []string {
	t.Helper()
	emptyCfg := filepath.Join(t.TempDir(), "empty-gitconfig")
	require.NoError(t, os.WriteFile(emptyCfg, nil, 0644))
	return append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+emptyCfg,
		"GIT_CONFIG_SYSTEM="+emptyCfg,
	)
}

// initGitRepo initializes a git repo in dir, adds all files, and commits them.
// All git commands run hermetically (see hermeticGitEnv).
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	initGitRepoWithConfig(t, dir, nil)
}

// initGitRepoWithConfig is initGitRepo with extra repo-local `git config`
// key/value pairs applied after init and before add/commit.
func initGitRepoWithConfig(t *testing.T, dir string, config [][2]string) {
	t.Helper()
	env := hermeticGitEnv(t)
	cmds := [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	}
	for _, kv := range config {
		cmds = append(cmds, []string{"config", kv[0], kv[1]})
	}
	cmds = append(cmds, []string{"add", "."}, []string{"commit", "-m", "initial"})
	for _, args := range cmds {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = env
		require.NoError(t, cmd.Run(), "git %v failed", args)
	}
}
