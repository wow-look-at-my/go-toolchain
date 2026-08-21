//go:build cosmo

package cmd

import (
	"syscall"
	"unsafe"
)

// This is the build that matters for the macOS bug: every released "linux"
// go-toolchain is a GOOS=cosmo fat APE, and running one on a Mac gives it no
// /proc, so inspectFD falls back to inspectFDStat -- which needs fstat and
// F_GETPATH. golang.org/x/sys/unix has no cosmo port (see
// claudeguard_tty_cosmo.go), so both go through the fork's stdlib syscall
// package instead.

// fdMode reports the descriptor's shape via fstat. The S_IF* constants are
// the same values on linux and darwin, which is what lets one cosmo binary
// classify correctly on either host.
func fdMode(fd uintptr) (fdKind, bool) {
	var st syscall.Stat_t
	if err := syscall.Fstat(int(fd), &st); err != nil {
		return fdUnknown, false
	}
	switch st.Mode & syscall.S_IFMT {
	case syscall.S_IFIFO:
		return fdFifo, true
	case syscall.S_IFSOCK:
		return fdSocket, true
	case syscall.S_IFCHR:
		return fdCharDevice, true
	case syscall.S_IFREG:
		return fdRegular, true
	}
	return fdUnknown, true
}

// fGetPath is darwin's F_GETPATH fcntl request. The fork's syscall package
// exports Fcntl but no F_GETPATH constant, so it is defined locally (the
// same arrangement as tcgets in claudeguard_tty_cosmo.go).
const fGetPath = 50

// maxPathLen is darwin's PATH_MAX, the buffer size F_GETPATH requires.
const maxPathLen = 1024

// fdPath names the file behind fd via fcntl(F_GETPATH), the procfs-free
// stand-in for reading /proc/self/fd/N. Returns "" on a host without that
// fcntl (linux), where the caller only loses the path in the message -- the
// descriptor still classifies as a file and is still refused.
func fdPath(fd uintptr) string {
	var buf [maxPathLen]byte
	if err := syscall.Fcntl(int(fd), fGetPath, uintptr(unsafe.Pointer(&buf[0]))); err != nil {
		return ""
	}
	n := 0
	for n < len(buf) && buf[n] != 0 {
		n++
	}
	return string(buf[:n])
}
