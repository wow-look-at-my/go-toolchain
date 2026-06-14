package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/go-toolchain/src/memlimit"
)

// setupGuardModule creates a throwaway git repo with a single main package and
// chdirs into it, returning the module root.
func setupGuardModule(t *testing.T) string {
	t.Helper()
	mod := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(mod, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module example.com/thing\n\ngo 1.19\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(mod, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644))

	orig, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(orig) })
	require.NoError(t, os.Chdir(mod))
	return mod
}

func TestInjectThenCleanupLeavesTreeClean(t *testing.T) {
	t.Setenv(memLimitEnvVar, "1") // force the feature on
	mod := setupGuardModule(t)
	guard := filepath.Join(mod, memlimit.GuardFileName)

	require.NoError(t, injectMemLimitGuard(true))

	// The guard was written for the build...
	_, err := os.Stat(guard)
	require.NoError(t, err, "guard should be injected before the build")

	// ...and it was gitignored so it can never trip the dirty-tree check.
	ignore, err := os.ReadFile(filepath.Join(mod, ".gitignore"))
	require.NoError(t, err)
	assert.Contains(t, string(ignore), memlimit.GuardFileName)

	cleanupMemLimitGuards()

	// The transient guard is gone afterward: nothing left behind.
	_, err = os.Stat(guard)
	assert.True(t, os.IsNotExist(err), "guard should be removed after the build")
}

func TestInjectGuardDisabledIsNoOp(t *testing.T) {
	t.Setenv(memLimitEnvVar, "off")
	mod := setupGuardModule(t)

	require.NoError(t, injectMemLimitGuard(true))

	_, err := os.Stat(filepath.Join(mod, memlimit.GuardFileName))
	assert.True(t, os.IsNotExist(err), "no guard should be written when the feature is off")

	// Cleanup with the feature off must not touch the tree either.
	cleanupMemLimitGuards()
}

func TestCleanupGuardDisabledLeavesGuard(t *testing.T) {
	mod := setupGuardModule(t)

	// Inject with the feature on, then attempt cleanup with it off: the kill
	// switch must make cleanup a no-op so a user who disables the feature keeps
	// whatever guard files they are managing themselves.
	t.Setenv(memLimitEnvVar, "1")
	require.NoError(t, injectMemLimitGuard(true))

	t.Setenv(memLimitEnvVar, "off")
	cleanupMemLimitGuards()

	_, err := os.Stat(filepath.Join(mod, memlimit.GuardFileName))
	assert.NoError(t, err, "cleanup must be a no-op when the feature is disabled")
}
