package cmd

import (
	"os"
	"strconv"
	"strings"
)

// readProcCmdline reads a process's argv from /proc, which stores it NUL
// separated with a trailing NUL. It is the reader a Linux host answers on,
// whether the build target is linux or the cosmo fat APE. No build tag selects
// it: readCmdline in guardcmdline.go measures the host instead, because the
// build target is cosmo on every host.
func readProcCmdline(pid int) ([]string, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil || len(b) == 0 {
		return nil, false
	}
	argv := strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
	return argv, len(argv) > 0
}
