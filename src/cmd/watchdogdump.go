//go:build unix || cosmo

package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// stallDumpAfter is the silence that makes the watchdog dump stacks. It is
// shorter than the CI step timeout, so a hung step prints where it waits.
var stallDumpAfter = 4 * time.Minute

// dumpStall writes this process's goroutines to w, then sends SIGQUIT to
// every descendant process. A Go child prints its goroutines and exits, so
// the build fails with the stack of the hang and does not wait for a timeout.
func dumpStall(w io.Writer, gap time.Duration) {
	fmt.Fprintf(w, "%s⚠ STALLED for %ds: dumping goroutines of go-toolchain (pid %d)%s\n",
		colorBoldRed, int(gap.Seconds()), os.Getpid(), colorReset)
	pprof.Lookup("goroutine").WriteTo(w, 2)

	pids, err := descendants(os.Getpid())
	if err != nil {
		fmt.Fprintf(w, "%s⚠ STALLED: cannot list child processes, so no child stacks: %v%s\n",
			colorBoldRed, err, colorReset)
		return
	}
	for _, pid := range pids {
		fmt.Fprintf(w, "%s⚠ STALLED: sending SIGQUIT to pid %d (%s) for its stack%s\n",
			colorBoldRed, pid, procName(pid), colorReset)
		if err := syscall.Kill(pid, syscall.SIGQUIT); err != nil {
			fmt.Fprintf(w, "%s⚠ STALLED: SIGQUIT to pid %d failed: %v%s\n", colorBoldRed, pid, err, colorReset)
		}
	}
}

// descendants returns the pids of all processes below root, read from /proc.
func descendants(root int) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	children := map[int][]int{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		ppid, ok := parentPid(pid)
		if !ok {
			continue
		}
		children[ppid] = append(children[ppid], pid)
	}
	var out []int
	queue := []int{root}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, c := range children[p] {
			out = append(out, c)
			queue = append(queue, c)
		}
	}
	return out, nil
}

// parentPid reads the ppid field of /proc/<pid>/stat. The comm field can hold
// spaces and parentheses, so the parse starts after the last ')'.
func parentPid(pid int) (int, bool) {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, false
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(f[1])
	return ppid, err == nil
}

// procName returns the command name of pid, or "?" when /proc has none.
func procName(pid int) string {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "comm"))
	if err != nil {
		return "?"
	}
	return strings.TrimSpace(string(b))
}
