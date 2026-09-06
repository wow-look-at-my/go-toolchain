//go:build !darwin

package cmd

import (
	"os"
	"strconv"
	"strings"
)

// readCmdline reads a process's argv from /proc, which stores it NUL
// separated with a trailing NUL.
func readCmdline(pid int) ([]string, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil || len(b) == 0 {
		return nil, false
	}
	argv := strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
	return argv, len(argv) > 0
}

func selfPID() int { return os.Getpid() }
