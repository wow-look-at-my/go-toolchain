package cmd

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Where a cosmo APE gets argv on a darwin host. A var, for a fake in a test.
var psBin = "/bin/ps"

// Bounds the whole invocation. A sandbox that refuses ps answers nothing.
const cmdlineProbeBudget = 2 * time.Second

// psCmdline reads a process's command line with ps.
func psCmdline(pid int) ([]string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), cmdlineProbeBudget)
	defer cancel()

	cmd := exec.CommandContext(ctx, psBin, "-ww", "-o", "command=", "-p", strconv.Itoa(pid))
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	argv := parsePSCommand(string(out))
	return argv, len(argv) > 0
}

// parsePSCommand turns a ps command line into the argv shellScript reads. ps
// prints a single string, so splitting it on spaces would lose the pipe that
// decides the classification. Whatever follows -c stays whole.
func parsePSCommand(line string) []string {
	line = strings.TrimRight(line, "\r\n")
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return fields
	}
	// The bundled-flag rule shellScript reads, so both agree.
	flag := fields[1]
	if !strings.HasPrefix(flag, "-") || strings.HasPrefix(flag, "--") || !strings.HasSuffix(flag, "c") {
		return fields
	}
	rest, ok := afterToken(line, fields[0], flag)
	if !ok {
		return fields
	}
	return []string{fields[0], flag, rest}
}

// afterToken returns what follows arg0 and flag in line, with no leading space.
func afterToken(line, arg0, flag string) (string, bool) {
	i := strings.Index(line, arg0)
	if i < 0 {
		return "", false
	}
	rest := line[i+len(arg0):]
	j := strings.Index(rest, flag)
	if j < 0 {
		return "", false
	}
	return strings.TrimLeft(rest[j+len(flag):], " \t"), true
}
