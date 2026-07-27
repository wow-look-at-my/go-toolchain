//go:build linux || cosmo

// This file is the Claude output guard's real classifier. It MUST build for
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
)

// claudeProcessAncestor reports whether any ancestor process is the Claude
// agent, identified by a process name beginning with "claude". It walks the
// parent-PID chain via /proc, stopping at PID 1 or after a bounded number of
// hops (defensive against pid-reuse races).
func claudeProcessAncestor() bool {
	pid := os.Getppid()
	for hops := 0; pid > 1 && hops < 64; hops++ {
		comm, ppid, ok := procCommPPID(pid)
		if !ok {
			return false
		}
		if strings.HasPrefix(comm, "claude") {
			return true
		}
		pid = ppid
	}
	return false
}

// isAncestorPID reports whether target is somewhere in this process's
// parent-PID chain.
func isAncestorPID(target int) bool {
	pid := os.Getppid()
	for hops := 0; pid > 1 && hops < 64; hops++ {
		if pid == target {
			return true
		}
		_, ppid, ok := procCommPPID(pid)
		if !ok {
			return false
		}
		pid = ppid
	}
	return pid == target
}

// procCommPPID reads /proc/<pid>/stat and returns the process's comm (the
// executable base name, truncated to 15 bytes by the kernel) and its parent
// PID. ok is false if the entry cannot be read or parsed.
func procCommPPID(pid int) (comm string, ppid int, ok bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", 0, false
	}
	s := string(data)
	// Field layout: "<pid> (<comm>) <state> <ppid> ...". comm may itself
	// contain spaces and parentheses, so anchor on the LAST ')' rather than
	// splitting the whole line into fields.
	open := strings.IndexByte(s, '(')
	closeParen := strings.LastIndexByte(s, ')')
	if open < 0 || closeParen < open {
		return "", 0, false
	}
	comm = s[open+1 : closeParen]
	fields := strings.Fields(s[closeParen+1:]) // [state, ppid, ...]
	if len(fields) < 2 {
		return "", 0, false
	}
	ppid, err = strconv.Atoi(fields[1])
	if err != nil {
		return "", 0, false
	}
	return comm, ppid, true
}

// inspectStdout classifies where go-toolchain's stdout (fd 1) is going, so the
// guard can refuse to run when Claude is hiding the output. It runs before the
// output watchdog rewires fd 1, so it sees the real descriptor the shell set up.
func inspectStdout() outputSink {
	return inspectFD(os.Stdout.Fd())
}

// inspectFD is inspectStdout's logic, parameterized on the descriptor so it can
// be tested against controlled pipes/files/devices.
func inspectFD(fd uintptr) outputSink {
	target, err := os.Readlink("/proc/self/fd/" + strconv.FormatUint(uint64(fd), 10))
	if err != nil {
		return outputSink{kind: sinkVisible} // can't tell — never block on uncertainty
	}

	switch {
	case strings.HasPrefix(target, "pipe:"):
		// A pipe is the agent hiding output (| head, | cat, $(...)) — UNLESS the
		// reader is the harness itself capturing our stdout, i.e. an ancestor
		// process named "claude". A filter in a shell pipeline is a sibling, and
		// a `$(...)` reader is a shell, so neither is an ancestor named claude.
		if name, pid, ok := pipePeerName(target); ok {
			if strings.HasPrefix(name, "claude") && isAncestorPID(pid) {
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
		if isHarnessCapturePath(target) {
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
		if isHarnessCapturePath(target) {
			return outputSink{kind: sinkVisible} // the harness's own transcript capture
		}
		return outputSink{kind: sinkFile, detail: target}
	}
	return outputSink{kind: sinkVisible} // unknown disposition — don't block
}

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
			if c, _, ok := procCommPPID(p); ok {
				return c, p, true
			}
			return "", p, true
		}
	}
	return "", 0, false
}
