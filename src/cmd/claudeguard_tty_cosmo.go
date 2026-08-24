//go:build cosmo

package cmd

import (
	"syscall"
	"unsafe"
)

// tcgets is the linux TCGETS ioctl request; the fork's cosmo syscall package exports Ioctl/Termios but no TCGETS constant.
const tcgets = 0x5401

// isTerminal mirrors the linux TCGETS check via the fork's stdlib syscall.
// Only reached on linux; darwin bails first (no /proc).
func isTerminal(fd uintptr) bool {
	var t syscall.Termios
	return syscall.Ioctl(int(fd), tcgets, uintptr(unsafe.Pointer(&t))) == nil
}
