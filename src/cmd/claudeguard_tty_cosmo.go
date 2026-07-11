//go:build cosmo

package cmd

import (
	"syscall"
	"unsafe"
)

// tcgets is the linux uapi TCGETS ioctl request (same value on amd64 and
// arm64). The gosmopolitan fork's cosmo syscall package exports Ioctl and
// Termios but no TCGETS constant, so it is defined locally.
const tcgets = 0x5401

// isTerminal reports whether fd refers to a terminal — TCGETS parity with the
// linux implementation, via the fork's stdlib syscall (golang.org/x/sys/unix
// has no cosmo port). This path is only reachable on linux hosts: on darwin
// the APE has no /proc, so inspectFD bails to sinkVisible before any
// char-device check.
func isTerminal(fd uintptr) bool {
	var t syscall.Termios
	return syscall.Ioctl(int(fd), tcgets, uintptr(unsafe.Pointer(&t))) == nil
}
