package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	t.Chdir(mod)
	return mod
}

func TestInjectThenCleanupLeavesTreeClean(t *testing.T) {
	t.Serial()
	mod := setupGuardModule(t)
	guard := filepath.Join(mod, memlimit.GuardFileName)

	require.NoError(t, injectMemLimitGuard(true))

	// The guard was written for the build...
	_, err := os.Stat(guard)
	require.NoError(t, err, "guard should be injected before the build")

	cleanupMemLimitGuards()

	// ...and it is gone afterward: nothing left behind in the working tree.
	_, err = os.Stat(guard)
	assert.True(t, os.IsNotExist(err), "guard should be removed after the build")
}

// Injection is unconditional: the old GO_TOOLCHAIN_AUTO_MEMLIMIT kill switch is
// gone, so a build that sets it (a stale export in a consumer's CI, say) must
// still get the guard rather than silently shipping unguarded binaries. The
// run-time GOMEMLIMIT=off escape hatch is the supported way to opt out.
func TestInjectGuardIgnoresRemovedKillSwitch(t *testing.T) {
	t.Serial()
	t.Setenv("GO_TOOLCHAIN_AUTO_MEMLIMIT", "off")
	mod := setupGuardModule(t)

	require.NoError(t, injectMemLimitGuard(true))

	_, err := os.Stat(filepath.Join(mod, memlimit.GuardFileName))
	assert.NoError(t, err, "the guard must be injected even when the removed kill switch is set")

	// Cleanup is unconditional too: the tree comes back clean.
	cleanupMemLimitGuards()
	_, err = os.Stat(filepath.Join(mod, memlimit.GuardFileName))
	assert.True(t, os.IsNotExist(err), "cleanup must remove the guard regardless of the removed env var")
}

// setupRealGitModule is setupGuardModule with a REAL (hermetic) git repository
// instead of a bare fake .git dir — needed by the exclude tests, whose code
// path shells out to `git rev-parse`.
func setupRealGitModule(t *testing.T) string {
	t.Helper()
	// Hermetic git: host/user config must not leak into the test repo.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	mod := t.TempDir()
	init := exec.Command("git", "init", "-q")
	init.Dir = mod
	require.NoError(t, init.Run())
	require.NoError(t, os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module example.com/thing\n\ngo 1.19\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(mod, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644))

	t.Chdir(mod)
	return mod
}

func readExclude(t *testing.T, mod string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(mod, ".git", "info", "exclude"))
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(t, err)
	return string(data)
}

// The headline invariant: while the injected guard exists on disk, git status
// must not see it — that is what Go's VCS stamping reads, and an untracked
// guard is what stamped every built binary "+dirty" on clean checkouts.
func TestInjectedGuardInvisibleToGitStatus(t *testing.T) {
	t.Serial()
	mod := setupRealGitModule(t)

	require.NoError(t, injectMemLimitGuard(true))
	_, err := os.Stat(filepath.Join(mod, memlimit.GuardFileName))
	require.NoError(t, err, "guard should be injected before the build")

	out, err := exec.Command("git", "status", "--porcelain").Output()
	require.NoError(t, err)
	assert.NotContains(t, string(out), memlimit.GuardFileName,
		"the in-flight guard must be invisible to git status (Go's vcs stamping reads it)")

	cleanupMemLimitGuards()
}

func TestEnsureGuardExcludedIsIdempotent(t *testing.T) {
	t.Serial()
	mod := setupRealGitModule(t)

	// Pre-existing operator content must be preserved, not clobbered.
	excl := filepath.Join(mod, ".git", "info", "exclude")
	require.NoError(t, os.MkdirAll(filepath.Dir(excl), 0o755))
	require.NoError(t, os.WriteFile(excl, []byte("*.scratch\n"), 0o644))

	ensureGuardExcluded()
	ensureGuardExcluded()

	got := readExclude(t, mod)
	assert.Contains(t, got, "*.scratch\n", "existing exclude entries must survive")
	assert.Equal(t, 1, strings.Count(got, memlimit.GuardFileName),
		"repeat runs must not duplicate the guard entry")
}

func TestEnsureGuardExcludedHandlesMissingTrailingNewline(t *testing.T) {
	t.Serial()
	mod := setupRealGitModule(t)
	excl := filepath.Join(mod, ".git", "info", "exclude")
	require.NoError(t, os.MkdirAll(filepath.Dir(excl), 0o755))
	require.NoError(t, os.WriteFile(excl, []byte("*.scratch"), 0o644)) // no trailing \n

	ensureGuardExcluded()

	got := readExclude(t, mod)
	assert.Contains(t, got, "*.scratch\n"+memlimit.GuardFileName+"\n",
		"the entry must land on its own line, not be glued to the previous one")
}

// Outside any repository the exclude step is a silent no-op — and so is the
// whole inject path, which must not fail the build over it.
func TestEnsureGuardExcludedNoRepoIsNoOp(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	t.Chdir(dir)
	// GIT_CEILING keeps discovery from escaping the temp dir if an outer repo surrounds it.
	t.Setenv("GIT_CEILING_DIRECTORIES", dir)

	ensureGuardExcluded() // must not panic, create files, or error

	_, statErr := os.Stat(filepath.Join(dir, ".git"))
	assert.True(t, os.IsNotExist(statErr), "no-repo case must not create anything")
}
