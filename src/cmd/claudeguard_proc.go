//go:build linux || cosmo

// This file is the agent output guard's real classifier. It MUST build for
// GOOS=cosmo as well as GOOS=linux: every released "linux" binary is a
// GOOS=cosmo fat-APE slot copy, and cosmo matches the `unix` build tag but
// not `linux` — a `_linux.go` filename (or a bare `//go:build linux`) would
// compile the guard out of every shipped binary while the GOOS=linux unit
// tests stay green (that was a real bug; claudeguard_buildtags_test.go pins
// the constraints). Everything here needs only /proc + stdlib, which work
// under cosmo on linux hosts. On a darwin host the APE has no /proc, so this
// classifier is blind and the guard cannot fire; that is a KNOWN GAP, not a
// design — see unclassifiableSink, which says so out loud, and
// docs/AGENT-OUTPUT-GUARD.md for what closing it needs. isTerminal is the one
// piece needing a platform ioctl and lives in claudeguard_tty_{linux,cosmo}.go.

package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/wow-look-at-my/go-toolchain/src/hostos"
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
		return unclassifiableSink()
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
		// A coding agent's own tool-execution plumbing (e.g. a socketpair for a
		// spawned child's stdio, which is what opencode's bash tool uses) looks
		// identical to a pipe from here — give it the same peer-identification
		// chance a pipe gets, rather than assuming hidden outright. detail always
		// carries something to show: the peer's name when resolved, else the raw
		// fd target, so the refusal message is never left with nothing to say.
		//
		// Unlike a pipe(), the two ends of an AF_UNIX socketpair are separate
		// sockets with DIFFERENT inodes — an fd-target string match can never
		// find the other end. SO_PEERCRED gives the kernel's own record of who
		// is on the other side, fixed at connection time, so it still resolves
		// after the parent (opencode/Node) closes its copy of the child's fd.
		if pid, ok := socketPeerPID(fd); ok {
			name, _, _ := agent.CommPPID(pid)
			if agent.IsPipeReader(name, pid) {
				return outputSink{kind: sinkVisible}
			}
			if name != "" {
				return outputSink{kind: sinkHidden, detail: name}
			}
			return outputSink{kind: sinkHidden, detail: target}
		}
		if name, pid, ok := pipePeerName(target); ok {
			if agent.IsPipeReader(name, pid) {
				return outputSink{kind: sinkVisible}
			}
			if name != "" {
				return outputSink{kind: sinkHidden, detail: name}
			}
		}
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

// unclassifiableSink answers for a descriptor this build cannot classify.
//
// On a linux host that is real uncertainty about one odd descriptor, and the
// guard must not block on it. On any other host it is not uncertainty at all:
// the APE has no /proc there, so NOTHING is classifiable and the guard is
// inoperative for the whole run. Both cases have to answer sinkVisible — a
// classifier that cannot see must not refuse a legitimate run — but only one
// of them is a guard that silently isn't running, and that one says so.
//
// The message is the notification that the platform's real classifier is
// missing. It is not a substitute for one; see docs/AGENT-OUTPUT-GUARD.md for
// what a darwin host still needs.
func unclassifiableSink() outputSink {
	if hostos.GOOS() != "linux" {
		warnGuardInoperative(hostos.GOOS())
	}
	return outputSink{kind: sinkVisible}
}

// warnGuardInoperative reports, once per run, that the output guard cannot
// classify anything on this host. It writes to the guard's own stderr writer
// for the same reason the refusal does: the guard fires precisely when stdout
// is captured, so stdout is the one place this must never go.
func warnGuardInoperative(host string) {
	guardInoperativeOnce.Do(func() {
		fmt.Fprintf(agentGuardOut, "\n%s⚠ go-toolchain's agent output guard is INOPERATIVE on this %s host.%s\n",
			colorBoldRed, host, colorReset)
		fmt.Fprintf(agentGuardOut, "This binary classifies stdout through /proc, which %s does not have, so it\n", host)
		fmt.Fprintf(agentGuardOut, "cannot tell whether its output is being captured and will not refuse a run\n")
		fmt.Fprintf(agentGuardOut, "that hides it. Read the output yourself; do not trust the guard here.\n\n")
	})
}

var guardInoperativeOnce sync.Once

// socketPeerPID (declared per-platform: claudeguard_sockpeer_linux.go uses
// golang.org/x/sys/unix, claudeguard_sockpeer_cosmo.go a raw syscall, since
// x/sys/unix has no cosmo port) returns the pid the kernel recorded as the
// other end of the AF_UNIX socket at fd, via SO_PEERCRED. For a socketpair(),
// that credential is fixed at creation time — the pid of whichever process
// called socketpair(), i.e. the real reader — so it still resolves after that
// process closes its own copy of the fd it handed the child (the normal thing
// for a coding agent's child_process to do, and why pipePeerName's inode match
// cannot see it). ok is false for anything that isn't a SOCK_STREAM/SOCK_DGRAM
// AF_UNIX socket, e.g. an anon_inode fd — never treated as a match.

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
