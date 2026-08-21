//go:build linux

package cmd

import "golang.org/x/sys/unix"

// fdMode reports the descriptor's shape via fstat.
func fdMode(fd uintptr) (fdKind, bool) {
	var st unix.Stat_t
	if err := unix.Fstat(int(fd), &st); err != nil {
		return fdUnknown, false
	}
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFIFO:
		return fdFifo, true
	case unix.S_IFSOCK:
		return fdSocket, true
	case unix.S_IFCHR:
		return fdCharDevice, true
	case unix.S_IFREG:
		return fdRegular, true
	}
	return fdUnknown, true
}

// fdPath names the file behind fd. On linux this branch is only reached when
// /proc is unavailable (a container without procfs mounted), where there is
// no second way to ask -- F_GETPATH is a darwin/BSD fcntl, not a linux one.
// An unnamed file still classifies as a file and is still refused; only the
// path in the message is missing.
func fdPath(uintptr) string { return "" }
