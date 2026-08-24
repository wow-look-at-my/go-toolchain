//go:build cosmo

// See claudeguard_proc.go's socketPeerPID doc comment. golang.org/x/sys/unix
// has no cosmo port (same reason claudeguard_tty_cosmo.go hand-rolls tcgets
// instead of using it), and the gosmopolitan fork's own syscall package
// exports SOL_SOCKET and a matching Ucred struct for cosmo but neither
// SO_PEERCRED nor a Getsockopt wrapper — so both are done directly via the
// raw SYS_GETSOCKOPT syscall, same pattern as tcgets.

package cmd

import (
	"syscall"
	"unsafe"
)

// soPeercred is the linux uapi SO_PEERCRED socket option, 0x11 on amd64 and arm64.
const soPeercred = 0x11

func socketPeerPID(fd uintptr) (pid int, ok bool) {
	var ucred syscall.Ucred
	size := uint32(syscall.SizeofUcred)
	_, _, errno := syscall.Syscall6(syscall.SYS_GETSOCKOPT, fd, uintptr(syscall.SOL_SOCKET), soPeercred,
		uintptr(unsafe.Pointer(&ucred)), uintptr(unsafe.Pointer(&size)), 0)
	if errno != 0 {
		return 0, false
	}
	return int(ucred.Pid), true
}
