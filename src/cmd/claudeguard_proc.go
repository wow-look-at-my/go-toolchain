//go:build linux || cosmo

// This file is the agent output guard's real classifier. It MUST build for
// GOOS=cosmo as well as GOOS=linux: every released "linux" binary is a
// GOOS=cosmo fat-APE slot copy, and cosmo matches the `unix` build tag but
// not `linux` — a `_linux.go` filename (or a bare `//go:build linux`) would
// compile the guard out of every shipped binary while the GOOS=linux unit
// tests stay green (that was a real bug; claudeguard_buildtags_test.go pins
// the constraints). Everything here needs only /proc + stdlib, which work
// under cosmo on linux hosts; on a darwin host the APE has no /proc, so
// inspectFD fails open to sinkVisible. isTerminal is the one piece needing a
// platform ioctl and lives in claudeguard_tty_{linux,cosmo}.go.

package cmd

import (
	"os"
	"strconv"
	"strings"

	agent "github.com/wow-look-at-my/is-this-an-agent"
)

// inspectStdout classifies where go-toolchain's stdout (fd 1) is going, so the
// guard can refuse to run when the agent is hiding the output. It runs before the
// output watchdog rewires fd 1, so it sees the real descriptor the shell set up.
func inspectStdout() outputSink {
	return inspectFD(os.Stdout.Fd())
}

// inspectFD is inspectStdout's logic, parameterized on the descriptor so it can
// be tested against controlled pipes/files/devices.
func inspectFD(fd uintptr) outputSink {
	target, err := os.Readlink("/proc/self/fd/" + strconv.FormatUint(uint64(fd), 10))
	if err != nil {
		// No /proc. This is the NORMAL case for a released binary: every
		// shipped "linux" go-toolchain is a GOOS=cosmo fat APE, and running
		// one on a macOS host gives it a kernel with no procfs at all.
		//
		// This used to `return sinkVisible` -- "can't tell, never block on
		// uncertainty" -- which meant the guard was dead on every Mac. It
		// was not uncertainty: `go-toolchain > out.txt` and
		// `go-toolchain | grep ...` were classified as visible and allowed,
		// which is precisely the hiding this guard exists to refuse.
		//
		// fstat + fcntl(F_GETPATH) answer the same question without procfs,
		// so fall back to them rather than failing open.
		return inspectFDStat(fd)
	}

	switch {
	case strings.HasPrefix(target, "pipe:"):
		// A pipe is the agent hiding output (| head, | cat, $(...)) — UNLESS the
		// reader is the harness itself capturing our stdout.
		if name, pid, ok := pipePeerName(target); ok {
			if agent.IsPipeReader(name, pid) {
				return outputSink{kind: sinkVisible}
			}
			return outputSink{kind: sinkPipe, detail: name}
		}
		return outputSink{kind: sinkPipe}
	case strings.HasPrefix(target, "socket:"), strings.HasPrefix(target, "anon_inode:"):
		return outputSink{kind: sinkHidden, detail: target}
	}

	// A path: classify by file type.
	fi, statErr := os.Stat(target)
	if statErr != nil {
		if agent.IsCapturePath(target) {
			return outputSink{kind: sinkVisible}
		}
		return outputSink{kind: sinkFile, detail: target}
	}
	mode := fi.Mode()
	switch {
	case mode&os.ModeCharDevice != 0:
		if isTerminal(fd) {
			return outputSink{kind: sinkVisible} // a real terminal — output is seen
		}
		return outputSink{kind: sinkDiscard, detail: target} // /dev/null and friends
	case mode&os.ModeNamedPipe != 0:
		return outputSink{kind: sinkPipe}
	case mode.IsRegular():
		if agent.IsCapturePath(target) {
			return outputSink{kind: sinkVisible} // the harness's own transcript capture
		}
		return outputSink{kind: sinkFile, detail: target}
	}
	return outputSink{kind: sinkVisible} // unknown disposition — don't block
}

// inspectFDStat classifies fd without procfs, for a cosmo APE running on a
// host that has none (macOS). fstat gives the descriptor's type and
// fcntl(F_GETPATH) its path, which together answer everything the /proc
// path answers except the NAME of a pipe's reader -- that needs the shared
// inode scan, so a pipe here is reported without a peer name.
//
// Not being able to name the reader does not weaken the decision: a pipe is
// refused either way, and the harness's own capture is recognized by
// IsCapturePath on the file path, which works fine here.
func inspectFDStat(fd uintptr) outputSink {
	mode, ok := fdMode(fd)
	if !ok {
		return outputSink{kind: sinkVisible} // genuinely cannot tell
	}
	switch mode {
	case fdFifo:
		return outputSink{kind: sinkPipe}
	case fdSocket:
		return outputSink{kind: sinkHidden, detail: "socket"}
	case fdCharDevice:
		if isTerminal(fd) {
			return outputSink{kind: sinkVisible}
		}
		return outputSink{kind: sinkDiscard, detail: fdPath(fd)}
	case fdRegular:
		path := fdPath(fd)
		if agent.IsCapturePath(path) {
			return outputSink{kind: sinkVisible}
		}
		return outputSink{kind: sinkFile, detail: path}
	}
	return outputSink{kind: sinkVisible}
}

// fdKind is the descriptor shape inspectFDStat cares about, named here so
// the syscall details stay in the per-platform files: golang.org/x/sys/unix
// has no cosmo port, so the cosmo build must reach the same answers through
// the gosmopolitan fork's stdlib syscall package instead.
type fdKind int

const (
	fdUnknown fdKind = iota
	fdFifo
	fdSocket
	fdCharDevice
	fdRegular
)

// pipePeerName returns the comm and pid of another process holding the same
// pipe as target ("pipe:[inode]"), i.e. the reader on the far end. Both ends of
// an anonymous pipe share one inode, so a process (other than us) whose fd
// symlinks to the same target is the consumer.
func pipePeerName(target string) (comm string, pid int, ok bool) {
	self := os.Getpid()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return "", 0, false
	}
	for _, e := range entries {
		p, err := strconv.Atoi(e.Name())
		if err != nil || p == self {
			continue
		}
		fddir := "/proc/" + e.Name() + "/fd"
		fds, err := os.ReadDir(fddir)
		if err != nil {
			continue
		}
		for _, f := range fds {
			link, err := os.Readlink(fddir + "/" + f.Name())
			if err != nil || link != target {
				continue
			}
			if c, _, ok := agent.CommPPID(p); ok {
				return c, p, true
			}
			return "", p, true
		}
	}
	return "", 0, false
}
