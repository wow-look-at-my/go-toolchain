//go:build linux

// See claudeguard_proc.go's socketPeerPID doc comment. This is the plain-linux
// implementation, backed by golang.org/x/sys/unix (no cosmo port, hence the
// separate claudeguard_sockpeer_cosmo.go).

package cmd

import "golang.org/x/sys/unix"

func socketPeerPID(fd uintptr) (pid int, ok bool) {
	ucred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return 0, false
	}
	return int(ucred.Pid), true
}
