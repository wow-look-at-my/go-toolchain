//go:build (linux && !cosmo) || darwin

package cache

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// While another live process holds the cache root's flock (a running FUSE
// daemon), the version purge must be deferred: no data deleted, no stamp
// written (so the next un-contended run performs it). flock locks are held per
// open file description, so a repeat open of the same lock file conflicts even
// within a process.
func TestEnsureLocalCacheVersion_DefersWhileRootIsOwned(t *testing.T) {
	root := t.TempDir()
	populateCacheRoot(t, root)

	owner, err := os.OpenFile(filepath.Join(root, fuseLockName), os.O_CREATE|os.O_RDWR, 0o644)
	require.NoError(t, err)
	defer owner.Close()
	require.NoError(t, syscall.Flock(int(owner.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))
	defer syscall.Flock(int(owner.Fd()), syscall.LOCK_UN)

	EnsureLocalCacheVersion(root)

	_, err = os.Stat(filepath.Join(root, "packs", "pack-000001.data"))
	require.NoError(t, err, "no purge may happen while another process owns the cache")
	_, err = os.Stat(filepath.Join(root, localCacheVersionFile))
	require.True(t, os.IsNotExist(err), "the stamp must stay unwritten so the next run retries")

	// As soon as the owner releases the lock, the deferred purge goes through.
	require.NoError(t, syscall.Flock(int(owner.Fd()), syscall.LOCK_UN))
	EnsureLocalCacheVersion(root)
	_, err = os.Stat(filepath.Join(root, "packs"))
	require.True(t, os.IsNotExist(err), "the deferred purge must run once the owner is gone")
	requireStampIs(t, root, currentLocalCacheVersion)
}
