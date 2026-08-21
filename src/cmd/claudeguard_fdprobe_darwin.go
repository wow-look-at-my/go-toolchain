//go:build darwin

// The same four descriptor probes as claudeguard_fdprobe_cosmo.go, for a
// NATIVE darwin build. Here the darwin constants are the right ones to spell
// -- there is no translating dispatcher in between, so SOL_LOCAL/LOCAL_PEERPID
// is the real API rather than the trap it would be in Linux-shaped code.
//
// A native build can always ask; supported is true throughout, and a failure
// is a real negative answer about the descriptor.

package cmd

import (
	"golang.org/x/sys/unix"
)

func fdFileTypeOnDarwinHost(fd uintptr) (mode uint32, supported bool) {
	var st unix.Stat_t
	if err := unix.Fstat(int(fd), &st); err != nil {
		return 0, false
	}
	return uint32(st.Mode) & unix.S_IFMT, true
}

func socketPeerOnDarwinHost(fd uintptr) (pid int, identified, supported bool) {
	p, ok := socketPeerPID(fd)
	return p, ok, true
}

func isTerminalOnDarwinHost(fd uintptr) (terminal, supported bool) {
	return isTerminal(fd), true
}

func fdPathOnDarwinHost(fd uintptr) (path string, supported bool) {
	return fdPath(fd), true
}
