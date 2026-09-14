package cmd

import (
	"os"
	"strconv"
	"strings"
)

// procCmdline reads a process's argv from /proc, which stores it NUL
// separated with a trailing NUL. It carries no build constraint: the cosmo
// APE picks between this and the ps reader at run time, so both are linked
// into it.
func procCmdline(pid int) ([]string, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil || len(b) == 0 {
		return nil, false
	}
	argv := strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
	return argv, len(argv) > 0
}
