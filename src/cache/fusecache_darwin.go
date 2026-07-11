//go:build darwin

package cache

import "golang.org/x/sys/unix"

// unmountDetach forces an unmount on macOS (which has no lazy-unmount flag).
// syscall.MNT_FORCE is not defined on darwin, so use golang.org/x/sys/unix.
// Used to clear a stale mount left by a crashed run.
const unmountDetach = unix.MNT_FORCE
