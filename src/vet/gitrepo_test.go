package vet

// Helpers for tests needing a real git repo (used here and in canonicalize_integration_test.go).

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// hermeticGitEnv points GIT_CONFIG_GLOBAL/SYSTEM at an empty file (not
// /dev/null -- portable to Windows) so the host's config (default branch,
// gpg signing, hooks, index.skipHash) can't leak into test repos.
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
