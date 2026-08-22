// darwin has no /proc, so classifying stdout does not reuse
// claudeguard_proc.go: fstat's mode bits answer pipe/regular/char-device
// without a path, isatty answers the terminal question (already vendored at
// src/compat/go-isatty), and F_GETPATH recovers a regular file's path -- the
// one thing fstat cannot give, needed for agent.IsCapturePath.
//
// A FIFO's reader is identified by walking ancestors for the other end of
// this pipe (proc_info on a native build, lsof from a cosmo APE). grok-build
// captures a child's stdout through Stdio::piped(), so that walk finds
// grok-build and the FIFO classifies as visible; `| cat` is a sibling and
// is not found, so it still fails CLOSED. A UNIX-domain socket is
// identified directly: getsockopt(SOL_LOCAL, LOCAL_PEERPID) gives the peer
// pid from the kernel, no ancestor walk needed -- a Node/Bun child_process
// typically wires stdio through a socketpair, not a bare pipe.

//go:build darwin

package cmd

import (
	"unsafe"

	"github.com/mattn/go-isatty"
	"golang.org/x/sys/unix"
)

// inspectStdout classifies where go-toolchain's stdout (fd 1) is going, so the
// guard can refuse to run when the agent is hiding the output.
func inspectStdout() outputSink {
	return inspectFD(uintptr(1))
}

// inspectFD is inspectStdout's logic, parameterized on the descriptor so it can
// be tested against controlled pipes/files/devices. The algorithm itself lives
// in claudeguard_darwinhost.go, shared with the cosmo APE that runs on this
// same host; a native build can always ask its probes, so the
// could-not-ask outcome degenerates to the old never-block-on-uncertainty.
func inspectFD(fd uintptr) outputSink {
	sink, ok := inspectFDDarwinHost(fd)
	if !ok {
		return outputSink{kind: sinkVisible}
	}
	return sink
}

// isTerminal reports whether fd is a real terminal, via the isatty package
// already vendored in this repo for its BSD/darwin support.
func isTerminal(fd uintptr) bool {
	return isatty.IsTerminal(fd)
}

// fdPath recovers an open descriptor's path via F_GETPATH -- darwin's answer
// to "what does this point at" absent /proc. Empty on any failure; callers
// must treat that as unknown, never as a free pass.
func fdPath(fd uintptr) string {
	buf := make([]byte, unix.PathMax)
	if _, err := unix.FcntlInt(fd, unix.F_GETPATH, int(uintptr(unsafe.Pointer(&buf[0])))); err != nil {
		return ""
	}
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}
	return string(buf)
}

// socketPeerPID returns the pid on the other end of a connected UNIX-domain
// socket fd, via getsockopt(SOL_LOCAL, LOCAL_PEERPID) -- a kernel-provided
// answer, not a guess, and the one piece of peer identification darwin gives
// for free without libproc.
func socketPeerPID(fd uintptr) (pid int, ok bool) {
	p, err := unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	if err != nil {
		return 0, false
	}
	return p, true
}
