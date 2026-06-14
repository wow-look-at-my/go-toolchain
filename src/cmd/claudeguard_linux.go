//go:build linux

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

// stdoutPipePeerName returns the process name on the read end of go-toolchain's
// stdout pipe, and true, when stdout is a pipe with an identifiable consumer.
func stdoutPipePeerName() (string, bool) {
	return pipePeer(os.Stdout.Fd())
}

// pipePeer resolves the pipe behind the given file descriptor and returns the
// comm of another process holding the same pipe inode (the reader on the far
// end). ok is false when fd is not a pipe or no peer process can be found.
//
// Both ends of an anonymous pipe share one inode, so scanning every process's
// fds for a symlink to the same "pipe:[inode]" target finds the consumer. We
// exclude our own PID; any remaining holder is the process reading our output.
func pipePeer(fd uintptr) (string, bool) {
	target, err := os.Readlink("/proc/self/fd/" + strconv.FormatUint(uint64(fd), 10))
	if err != nil || !strings.HasPrefix(target, "pipe:") {
		return "", false
	}
	self := os.Getpid()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue // non-numeric /proc entry, or ourselves
		}
		fddir := "/proc/" + e.Name() + "/fd"
		fds, err := os.ReadDir(fddir)
		if err != nil {
			continue // process exited or fd dir not readable
		}
		for _, f := range fds {
			link, err := os.Readlink(fddir + "/" + f.Name())
			if err != nil || link != target {
				continue
			}
			if comm, _, ok := procCommPPID(pid); ok {
				return comm, true
			}
			return "", false
		}
	}
	return "", false
}
