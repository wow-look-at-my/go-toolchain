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

// The stand-in writes its arguments to a file, which is what each test below
// reads.
func writeForkBranchScript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, forkSubmoduleDir, filepath.FromSlash(forkBranchScript))
	require.NoError(t, os.MkdirAll(filepath.Dir(script), 0o755))
	record := filepath.Join(dir, "said")
	body := "#!/usr/bin/env bash\nprintf '%s' \"$*\" > " + record + "\n"
	require.NoError(t, os.WriteFile(script, []byte(body), 0o755))
	t.Chdir(dir)
	return record
}

func TestBranchForkSubmodulesNamesTheBranch(t *testing.T) {
	record := writeForkBranchScript(t)
	for _, args := range [][]string{
		{"init", "--initial-branch", "claude/pin"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
		{"commit", "--allow-empty", "-m", "first"},
	} {
		require.NoError(t, exec.Command("git", args...).Run())
	}

	require.NoError(t, branchForkSubmodules(runner.New()))

	said, err := os.ReadFile(record)
	require.NoError(t, err)
	assert.Equal(t, "claude/pin", string(said))
}

func TestBranchForkSubmodulesOutsideARepository(t *testing.T) {
	record := writeForkBranchScript(t)

	require.NoError(t, branchForkSubmodules(runner.New()))

	said, err := os.ReadFile(record)
	require.NoError(t, err)
	assert.Empty(t, string(said))
}

func TestBranchForkSubmodulesWithoutTheScript(t *testing.T) {
	t.Chdir(t.TempDir())

	err := branchForkSubmodules(runner.New())

	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), forkBranchScript), err.Error())
}
