//go:build darwin

package cache

import "syscall"

// unmountDetach forces an unmount on macOS (which has no lazy-unmount flag).
// Used to clear a stale mount left by a crashed run.
const unmountDetach = syscall.MNT_FORCE
