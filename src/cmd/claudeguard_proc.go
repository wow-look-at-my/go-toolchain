//go:build linux || cosmo

// This file is the agent output guard's real classifier. It MUST build for
// GOOS=cosmo as well as GOOS=linux: every released "linux" binary is a
// GOOS=cosmo fat-APE slot copy, and cosmo matches the `unix` build tag but
// not `linux` — a `_linux.go` filename (or a bare `//go:build linux`) would
// compile the guard out of every shipped binary while the GOOS=linux unit
// tests stay green (that was a real bug; claudeguard_buildtags_test.go pins
// the constraints). Everything here needs only /proc + stdlib, which work
// under cosmo on linux hosts. On a darwin or NT host the APE has no /proc, so
// this classifier is blind and the guard cannot fire; that is a KNOWN GAP, not a
// design — see unclassifiableSink, which says so out loud, and
// docs/AGENT-OUTPUT-GUARD.md for what closing it needs. A character device is
// decided from its own path (isTerminalDevicePath below): this build carries
// no platform ioctl helper.

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

// inspectStdout reads the raw descriptor: logx.Install() swaps os.Stdout
// for a pipe, so Fd() would misreport a real terminal as hidden.
func inspectStdout() outputSink {
	return inspectFD(1)
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
			if harnessIsPipeReader(name, pid) {
				return outputSink{kind: sinkVisible}
			}
			return outputSink{kind: sinkPipe, detail: name}
		}
		return unnamedPeerSink()
	case strings.HasPrefix(target, "socket:"), strings.HasPrefix(target, "anon_inode:"):
		// A socketpair looks like a pipe here -- give it the same peer-ID
		// chance rather than assuming hidden. A socketpair's ends are separate
		// sockets with different inodes, so an fd-target match usually cannot
		// find the other end; the unnameable-reader fallback -- the spawning
		// command line -- decides.
		if name, pid, ok := pipePeerName(target); ok {
			if harnessIsPipeReader(name, pid) {
				return outputSink{kind: sinkVisible}
			}
			if name != "" {
				return outputSink{kind: sinkHidden, detail: name}
			}
		}
		return unnamedPeerSink()
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
		// No platform ioctl in this build, so the device's own path answers:
		// a tty spelling is a real terminal, anything else (/dev/null and
		// friends) is a discard.
		if isTerminalDevicePath(target) {
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

// unclassifiableSink: an unreadable fd allows silently; no classifier at
// all warns and allows -- a future classifier should refuse instead.
func unclassifiableSink() outputSink {
	if host := hostos.GOOS(); host != "linux" {
		return blindClassifierSink(host)
	}
	return unreadableDescriptorSink()
}

// unreadableDescriptorSink answers for a descriptor that could not be read
// on a host whose classifier otherwise works.
func unreadableDescriptorSink() outputSink {
	return outputSink{kind: sinkVisible}
}

func unnamedPeerSink() outputSink { return unidentifiedPeerSink(sinkPipe) }

// blindClassifierSink answers on a host where this build has no classifier at
// all, and announces that the guard is not running.
func blindClassifierSink(host string) outputSink {
	warnGuardInoperative(host)
	return outputSink{kind: sinkVisible}
}

// Reports a single time per run that the guard is blind on this host, via the
// guard's stderr writer -- stdout is what it fires on capturing.
func warnGuardInoperative(host string) {
	guardInoperativeOnce.Do(func() {
		fmt.Fprintf(agentGuardOut, guardInoperativeBanner, colorBoldRed, host, colorReset, host)
	})
}

var guardInoperativeOnce sync.Once

// guardInoperativeBanner is the warning, held as a document. Its values are
// the colours and the host, which it names in both the headline and the body.
const guardInoperativeBanner = "\n%s⚠ go-toolchain's agent output guard is INOPERATIVE on this %s host.%s\n" +
	"This binary classifies stdout through /proc, which %s does not have, so it\n" +
	"cannot tell whether its output is being captured and will not refuse a run\n" +
	"that hides it. Read the output yourself; do not trust the guard here.\n\n"

// isTerminalDevicePath reports whether a character device's path names a
// terminal: a pty slave spells /dev/pts/N or /dev/ttyN, and the console and
// pty master keep their own well-known names. Path-based because this build
// carries no platform ioctl helper.
func isTerminalDevicePath(path string) bool {
	switch {
	case strings.HasPrefix(path, "/dev/tty"), strings.HasPrefix(path, "/dev/pts/"):
		return true
	case path == "/dev/console", path == "/dev/ptmx":
		return true
	}
	return false
}

// pipePeerName returns the comm and pid of another process holding the same
// pipe as target ("pipe:[inode]"), i.e. the reader on the far end. Both ends
// of an anonymous pipe share an inode, so a match here can be the write end
// too -- fdAccessMode below screens those out.
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
			if mode, modeOK := fdAccessMode(p, f.Name()); modeOK && mode == oAccModeWriteOnly {
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

// oAccModeMask and oAccModeWriteOnly are O_ACCMODE and O_WRONLY: fixed POSIX
// values, read from /proc/pid/fdinfo.
const (
	oAccModeMask      = 0x3
	oAccModeWriteOnly = 0x1
)

// fdAccessMode reads pid's fd's O_ACCMODE bits. A shell forks a command and
// keeps its own stdout fd open, matching the same pipe string pipePeerName
// scans for; this tells that write-end copy apart from the real reader.
func fdAccessMode(pid int, fdName string) (mode int, ok bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/fdinfo/" + fdName)
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		rest, found := strings.CutPrefix(line, "flags:")
		if !found {
			continue
		}
		flags, err := strconv.ParseInt(strings.TrimSpace(rest), 8, 64)
		if err != nil {
			return 0, false
		}
		return int(flags) & oAccModeMask, true
	}
	return 0, false
}
