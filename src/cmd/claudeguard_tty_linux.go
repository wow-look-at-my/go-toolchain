//go:build linux

package cmd

import "golang.org/x/sys/unix"

// isTerminal reports whether fd refers to a terminal.
func isTerminal(fd uintptr) bool {
	_, err := unix.IoctlGetTermios(int(fd), unix.TCGETS)
	return err == nil
}
