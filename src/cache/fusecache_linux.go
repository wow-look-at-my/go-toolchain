//go:build linux && !cosmo

package cache

import "syscall"

// unmountDetach performs a lazy unmount on Linux: detach the filesystem now and
// clean up when it's no longer busy. Used to clear a stale mount left by a
// crashed run.
const unmountDetach = syscall.MNT_DETACH
