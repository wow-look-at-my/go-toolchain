// darwin has no /proc, so classifying stdout does not reuse
// claudeguard_proc.go: fstat's mode bits answer pipe/regular/char-device
// without a path, isatty answers the terminal question (already vendored at
// src/compat/go-isatty), and F_GETPATH recovers a regular file's path -- the
// one thing fstat cannot give, needed for agent.IsCapturePath.
//
// A pipe's reader cannot be identified here (that needs libproc, not
// implemented): unlike linux/cosmo, an agent piping its own subprocess's
// stdout back to itself is indistinguishable from `| grep` doing the same, so
// every pipe fails CLOSED here -- the same fail-closed rule already applied
// to an agent renamed beyond its roster prefixes.

//go:build darwin

package cmd

import (
	"unsafe"

	"github.com/mattn/go-isatty"
	"golang.org/x/sys/unix"

	agent "github.com/wow-look-at-my/is-this-an-agent"
)

// inspectStdout classifies where go-toolchain's stdout (fd 1) is going, so the
// guard can refuse to run when the agent is hiding the output.
func inspectStdout() outputSink {
	return inspectFD(uintptr(1))
}

// inspectFD is inspectStdout's logic, parameterized on the descriptor so it can
// be tested against controlled pipes/files/devices.
func inspectFD(fd uintptr) outputSink {
	var st unix.Stat_t
	if err := unix.Fstat(int(fd), &st); err != nil {
		return outputSink{kind: sinkVisible} // can't tell -- never block on uncertainty
	}

	switch uint32(st.Mode) & unix.S_IFMT {
	case unix.S_IFIFO:
		// See the file header: the reader on the far end cannot be identified
		// on darwin, so every pipe is treated as hiding the output.
		return outputSink{kind: sinkPipe}
	case unix.S_IFSOCK:
		return outputSink{kind: sinkHidden}
	case unix.S_IFCHR:
		if isTerminal(fd) {
			return outputSink{kind: sinkVisible} // a real terminal -- output is seen
		}
		return outputSink{kind: sinkDiscard, detail: fdPath(fd)} // /dev/null and friends
	case unix.S_IFREG:
		path := fdPath(fd)
		if path != "" && agent.IsCapturePath(path) {
			return outputSink{kind: sinkVisible} // the harness's own transcript capture
		}
		return outputSink{kind: sinkFile, detail: path}
	default:
		return outputSink{kind: sinkVisible} // unknown disposition -- don't block
	}
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

// pipePeerName cannot identify a pipe's reader without libproc (unimplemented
// here), so it always answers unknown. The symbol exists so
// claudeguard_test.go compiles on every platform --
// TestPipePeerNameDetectsConsumer skips itself outside linux.
func pipePeerName(string) (comm string, pid int, ok bool) {
	return "", 0, false
}
