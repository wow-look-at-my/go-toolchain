//go:build cosmo

// The four descriptor probes inspectFDDarwinHost needs, for a cosmo APE
// running on a darwin host.
//
// These are written as ORDINARY LINUX-SHAPED syscalls and the gosmopolitan
// dispatcher translates them to Apple's equivalents. Two consequences worth
// stating, because guessing either way produces silently wrong answers:
//
//   - The peer credential is asked for as SOL_SOCKET/SO_PEERCRED, never as
//     darwin's SOL_LOCAL/LOCAL_PEERPID. Level 0 is IPPROTO_IP on Linux and
//     LOCAL_PEERPID is 2, which is IP_TTL -- spelling the darwin pair here
//     would turn a peer-pid query into a TTL query on a Linux host, silently.
//   - The variadic ABI is NOT this module's problem. The dispatcher applies
//     the arm64-apple stack-passing fix internally; runtime.cosmoLibcCallVariadic1
//     is runtime-internal and unreachable from here by design.
//
// Until the fork supports these on darwin the dispatcher answers ENOSYS /
// ENOPROTOOPT, which each probe reports as UNSUPPORTED rather than as a
// negative answer -- so the classifier goes blind and says so, instead of
// refusing a legitimate run or waving a captured one through.

package cmd

import (
	"syscall"
	"unsafe"
)

// fGetPath is Apple's F_GETPATH fcntl command. This is the one probe with no
// Linux counterpart to be shaped like, so the Apple number is correct here;
// the fork accepts it specifically.
const fGetPath = 50

// pathMax bounds the F_GETPATH buffer (darwin's PATH_MAX).
const pathMax = 1024

// unsupportedErrno reports whether errno means "this build cannot ask that
// question here", as opposed to a real negative answer about the descriptor.
func unsupportedErrno(errno syscall.Errno) bool {
	switch errno {
	case syscall.ENOSYS, syscall.ENOTSUP, syscall.EOPNOTSUPP, syscall.ENOPROTOOPT, syscall.EINVAL:
		return true
	}
	return false
}

// fdFileTypeOnDarwinHost returns fd's S_IFMT file-type bits.
func fdFileTypeOnDarwinHost(fd uintptr) (mode uint32, supported bool) {
	var st syscall.Stat_t
	if err := syscall.Fstat(int(fd), &st); err != nil {
		return 0, false
	}
	return uint32(st.Mode) & syscall.S_IFMT, true
}

// socketPeerOnDarwinHost returns the pid the kernel recorded on the far end of
// a connected socket.
func socketPeerOnDarwinHost(fd uintptr) (pid int, identified, supported bool) {
	var ucred syscall.Ucred
	size := uint32(syscall.SizeofUcred)
	_, _, errno := syscall.Syscall6(syscall.SYS_GETSOCKOPT, fd, uintptr(syscall.SOL_SOCKET), soPeercred,
		uintptr(unsafe.Pointer(&ucred)), uintptr(unsafe.Pointer(&size)), 0)
	if errno != 0 {
		return 0, false, !unsupportedErrno(errno)
	}
	return int(ucred.Pid), true, true
}

// isTerminalOnDarwinHost reports whether fd is a real terminal. ENOTTY is the
// answer "no", not a missing capability.
func isTerminalOnDarwinHost(fd uintptr) (terminal, supported bool) {
	var t syscall.Termios
	errno := syscall.Ioctl(int(fd), tcgets, uintptr(unsafe.Pointer(&t)))
	if errno == nil {
		return true, true
	}
	if e, isErrno := errno.(syscall.Errno); isErrno {
		if e == syscall.ENOTTY {
			return false, true
		}
		return false, !unsupportedErrno(e)
	}
	return false, false
}

// fdPathOnDarwinHost recovers an open descriptor's path -- darwin's answer to
// what /proc/self/fd's readlink gives on Linux.
func fdPathOnDarwinHost(fd uintptr) (path string, supported bool) {
	buf := make([]byte, pathMax)
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, fGetPath, uintptr(unsafe.Pointer(&buf[0])))
	if errno != 0 {
		return "", !unsupportedErrno(errno)
	}
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i]), true
		}
	}
	return string(buf), true
}
