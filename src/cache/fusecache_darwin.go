//go:build darwin

package cache

import "golang.org/x/sys/unix"

// unmountDetach forces an unmount to clear a stale mount from a crashed run (macOS has no lazy-unmount flag).
const unmountDetach = unix.MNT_FORCE
