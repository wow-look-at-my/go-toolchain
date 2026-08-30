package vet

// Helpers for tests needing a real git repo (used here and in canonicalize_integration_test.go).

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = env
		require.NoError(t, cmd.Run(), "git %v failed", args)
	}
	run("init")

	// The settings go straight into .git/config rather than through one `git
	// config` per key. Every one of these repos costs a process per key
	// otherwise, and this package builds ten of them.
	settings := append([][2]string{
		{"user.email", "test@test.com"},
		{"user.name", "Test"},
		{"commit.gpgsign", "false"},
	}, config...)
	appendRepoConfig(t, dir, settings)

	run("add", ".")
	run("commit", "-m", "initial")
}

// appendRepoConfig writes section.key settings into an existing repo's config.
// git reads repeated sections, so appending needs no merge.
func appendRepoConfig(t *testing.T, dir string, settings [][2]string) {
	t.Helper()
	var b strings.Builder
	for _, kv := range settings {
		section, key, ok := strings.Cut(kv[0], ".")
		require.True(t, ok, "config key %q is not section.key", kv[0])
		fmt.Fprintf(&b, "[%s]\n\t%s = %s\n", section, key, kv[1])
	}
	path := filepath.Join(dir, ".git", "config")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	require.NoError(t, err)
	_, werr := f.WriteString(b.String())
	require.NoError(t, f.Close())
	require.NoError(t, werr)
}
