//go:build darwin

// FIFO peer identification on a native darwin build: proc_info(PROC_PIDFDPIPEINFO)
// gives each pipe end a handle and the handle of the other end, and
// PROC_PIDLISTFDS lists an ancestor's descriptors. grok-build's Stdio::piped()
// capture is this: the agent holds the read end, the child holds the write
// end, and `| cat` is a sibling that this ancestor walk will not see.

package cmd

import (
	"os"
	"unsafe"

	agent "github.com/wow-look-at-my/is-this-an-agent"
	"golang.org/x/sys/unix"
)

const (
	procInfoCallPIDInfo   = 2
	procInfoCallPIDFDInfo = 3
	procPIDListFDs        = 1
	procPIDFDPipeInfo     = 6
	proxFDTypePipe        = 6
	pipeFDInfoSize        = 184
	pipeHandleOff         = 160
	pipePeerHandleOff     = 168
	maxProcFDs            = 4096
	maxAncestorHops       = 64
)

// fifoPeerOnDarwinHost returns the ancestor pid holding the other end of the
// FIFO at fd. supported is always true: a failed probe is "not identified",
// which classifyDarwinFD fails closed, never a missing-capability blind.
func fifoPeerOnDarwinHost(fd uintptr) (pid int, identified, supported bool) {
	_, peer, ok := pipeHandles(os.Getpid(), int(fd))
	if !ok {
		return 0, false, true
	}
	p := os.Getppid()
	for hops := 0; p > 1 && hops < maxAncestorHops; hops++ {
		if pidHasPipeHandle(p, peer) {
			return p, true, true
		}
		_, ppid, ok := agent.CommPPID(p)
		if !ok {
			break
		}
		p = ppid
	}
	return 0, false, true
}

func pipeHandles(pid, fd int) (handle, peer uint64, ok bool) {
	buf := make([]byte, pipeFDInfoSize)
	n, errno := procInfo(procInfoCallPIDFDInfo, pid, procPIDFDPipeInfo, uint64(fd), buf)
	if errno != 0 || n < pipeFDInfoSize {
		return 0, 0, false
	}
	handle = *(*uint64)(unsafe.Pointer(&buf[pipeHandleOff]))
	peer = *(*uint64)(unsafe.Pointer(&buf[pipePeerHandleOff]))
	return handle, peer, handle != 0 || peer != 0
}

func pidHasPipeHandle(pid int, want uint64) bool {
	buf := make([]byte, maxProcFDs*8)
	n, errno := procInfo(procInfoCallPIDInfo, pid, procPIDListFDs, 0, buf)
	if errno != 0 || n < 8 {
		return false
	}
	count := n / 8
	if count > maxProcFDs {
		count = maxProcFDs
	}
	for i := 0; i < count; i++ {
		info := *(*struct {
			FD   int32
			Type uint32
		})(unsafe.Pointer(&buf[i*8]))
		if info.Type != proxFDTypePipe {
			continue
		}
		handle, _, ok := pipeHandles(pid, int(info.FD))
		if ok && handle == want {
			return true
		}
	}
	return false
}

func procInfo(callnum, pid, flavor int, arg uint64, buf []byte) (int, unix.Errno) {
	r1, _, errno := unix.Syscall6(
		unix.SYS_PROC_INFO,
		uintptr(callnum),
		uintptr(pid),
		uintptr(flavor),
		uintptr(arg),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	return int(r1), errno
}
