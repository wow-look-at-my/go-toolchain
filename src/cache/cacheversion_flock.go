//go:build (linux && !cosmo) || darwin

package cache

import (
	"os"
	"path/filepath"
	"syscall"
)

// lockCacheRootForPurge takes the exclusive, non-blocking flock on root's
// fuseLockName — the exact interlock a live FUSE daemon holds for its lifetime
// (see newFuseCache) — so the version purge never deletes pack files out from
// under a live owner. Returns a release func and ok == true when the lock was
// acquired; ok == false means another live process owns the cache (or the lock
// file is unusable) and the purge must be skipped.
func lockCacheRootForPurge(root string) (release func(), ok bool) {
	f, err := os.OpenFile(filepath.Join(root, fuseLockName), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, false
	}
	return func() { releaseLock(f) }, true
}
