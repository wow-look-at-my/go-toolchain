//go:build unix || cosmo

// stallDumpAfter is the silence that makes the watchdog dump stacks.

package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime/pprof"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// stallDumpAfter is the silence that makes the watchdog dump stacks.
var stallDumpAfter = 4 * time.Minute

// A child that gets the SIGQUIT prints every goroutine, not only the one that runs.
func init() {
	if os.Getenv("GOTRACEBACK") == "" {
		os.Setenv("GOTRACEBACK", "all")
	}
}

// dumpStall writes this process's goroutines to w, then sends SIGQUIT to
// every descendant process. A Go child prints its goroutines and exits, so
// the build fails with the stack of the hang and does not wait for a timeout.
func dumpStall(w io.Writer, gap time.Duration) {
	fmt.Fprintf(w, "%s⚠ STALLED for %ds: dumping goroutines of go-toolchain (pid %d)%s\n",
		colorBoldRed, int(gap.Seconds()), os.Getpid(), colorReset)
	pprof.Lookup("goroutine").WriteTo(w, 2)

	procs, err := descendants(os.Getpid())
	if err != nil {
		fmt.Fprintf(w, "%s⚠ STALLED: cannot list child processes, so no child stacks: %v%s\n",
			colorBoldRed, err, colorReset)
		return
	}
	for _, p := range procs {
		err := syscall.Kill(p.pid, syscall.SIGQUIT)
		switch {
		case err == syscall.ESRCH:
			// The process ended after ps listed it. The ps process itself is one of these.
		case err != nil:
			fmt.Fprintf(w, "%s⚠ STALLED: SIGQUIT to pid %d (%s) failed: %v%s\n",
				colorBoldRed, p.pid, p.name, err, colorReset)
		default:
			fmt.Fprintf(w, "%s⚠ STALLED: sent SIGQUIT to pid %d (%s) for its stack%s\n",
				colorBoldRed, p.pid, p.name, colorReset)
		}
	}
}

type proc struct {
	pid  int
	name string
}

// descendants returns all processes below root. It reads the process table
// from ps, because macOS has no /proc.
func descendants(root int) ([]proc, error) {
	out, err := exec.Command("ps", "-A", "-o", "pid=", "-o", "ppid=", "-o", "comm=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	children := map[int][]proc{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		children[ppid] = append(children[ppid], proc{pid, strings.Join(f[2:], " ")})
	}
	var found []proc
	queue := []int{root}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, c := range children[p] {
			found = append(found, c)
			queue = append(queue, c.pid)
		}
	}
	return found, nil
}
