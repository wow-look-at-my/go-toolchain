// guardcmdline_ps.go holds the guardcmdline ps code.

package cmd

import (
	"os/exec"
	"strconv"
	"strings"
)

// readPSCmdline asks ps for a process's command line. A host without /proc
// offers nothing better, and ps is already how this pipeline lists processes it
// cannot read directly (watchdogdump.go, for the same macOS reason).
func readPSCmdline(pid int) ([]string, bool) {
	// -ww defeats ps's own column limit, which would cut a long script short.
	out, err := exec.Command("ps", "-ww", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return nil, false
	}
	// One pid is one line of argv, and a header a ps prints anyway comes first.
	line := ""
	for _, l := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(l) != "" {
			line = l
		}
	}
	if line == "" {
		return nil, false
	}
	return psArgv(line)
}

// psArgv recovers argv from one line of ps output. Whitespace separates
// arguments, so a shell's command string -- the last argument, and the
// argument that holds spaces of its own. Whitespace is put back together
// as a unit.
func psArgv(line string) ([]string, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return nil, false
	}
	i := psScriptIndex(fields)
	if i < 0 {
		return fields, true
	}
	return append(fields[:i], strings.Join(fields[i:], " ")), true
}

// psScriptIndex names the first field of a shell command string, or a negative
// answer when the line is not a shell handed one.
func psScriptIndex(fields []string) int {
	if len(fields) < 3 || !isShell(fields[0]) {
		return -1
	}
	for i, a := range fields[1:] {
		// A command string is the whole tail, so a shell with none is not one.
		if takesCommandString(a) && i+2 < len(fields) {
			return i + 2
		}
	}
	return -1
}
