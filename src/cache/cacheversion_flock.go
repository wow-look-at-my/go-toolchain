//go:build (linux && !cosmo) || darwin

package cache

import (
	"os"
	"path/filepath"
	"syscall"
)

// lockCacheRootForPurge takes the exclusive, non-blocking flock on root's
// fuseLockName -- the same lock a live FUSE daemon holds -- so a purge
// never deletes packs from under a live owner. ok is false when another
// process owns the cache or the lock file is unusable; skip the purge.
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
