//go:build linux && !cosmo

package cache

import "syscall"

// unmountDetach: lazy unmount, detaching now and cleaning up when no longer busy, for a stale crashed-run mount.
const unmountDetach = syscall.MNT_DETACH
