//go:build darwin || cosmo

// The ps(1)-backed argv read: what a cosmo APE has on a macOS host. There is
// no /proc there, and the KERN_PROCARGS2 sysctl the native darwin build uses
// answers ENOSYS from a cosmo binary, so the host's own tool reads argv
// instead. Compiled on darwin as well as cosmo, which is how the reader no CI
// runner exercises in its real configuration still gets tested.

package cmd

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// psCmdlineBin is absolute so the read does not depend on PATH.
const psCmdlineBin = "/bin/ps"

// psCmdlineTimeout bounds a lookup. ps answers at once or not at all, and
// this runs ahead of every other thing the process does.
const psCmdlineTimeout = 5 * time.Second

// readCmdlinePS reads a process's command line with ps.
func readCmdlinePS(pid int) ([]string, bool) {
	if pid <= 0 {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), psCmdlineTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, psCmdlineBin, "-ww", "-o", "args=", "-p", strconv.Itoa(pid))
	// Killing ps at the deadline leaves Wait blocked while the stdout pipe
	// stays open, which is what WaitDelay closes.
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	return parsePSArgs(string(out))
}

// parsePSArgs splits a `ps -o args=` row into argv. ps prints the arguments
// space separated with the shell's quoting already gone, so the boundaries
// inside a shell's command string are unrecoverable. Everything past a
// shell's -c is therefore returned unsplit: that flag takes a whole command
// string, and the spaces in it are the script's own.
func parsePSArgs(out string) ([]string, bool) {
	line, _, _ := strings.Cut(out, "\n")
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return nil, false
	}
	if !isShell(fields[0]) {
		return fields, true
	}
	for i, f := range fields[1:] {
		if !takesCommandString(f) || i+2 >= len(fields) {
			continue
		}
		argv := append([]string{}, fields[:i+2]...)
		return append(argv, strings.Join(fields[i+2:], " ")), true
	}
	return fields, true
}
