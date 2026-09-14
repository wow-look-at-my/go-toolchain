//go:build darwin || cosmo

// There is no /proc on a darwin host, and the KERN_PROCARGS2 sysctl the
// native darwin build uses answers ENOSYS from a cosmo binary, so the host's
// own tool reads argv instead. Compiled on darwin as well as cosmo, which is
// how the reader no CI runner exercises in its real configuration still gets
// tested.

package cmd

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Where a cosmo APE gets argv on a darwin host. Absolute, so the read does
// not depend on PATH. A var, for a fake in a test.
var psBin = "/bin/ps"

// Bounds the whole invocation. A sandbox that refuses ps answers nothing.
const cmdlineProbeBudget = 2 * time.Second

// psCmdline reads a process's command line with ps.
func psCmdline(pid int) ([]string, bool) {
	if pid <= 0 {
		lastCmdlineProbeErr = strconv.Itoa(pid) + " is not a pid, so nothing was asked of " + psBin
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), cmdlineProbeBudget)
	defer cancel()

	cmd := exec.CommandContext(ctx, psBin, "-ww", "-o", "command=", "-p", strconv.Itoa(pid))
	// Killing ps at the deadline leaves Wait blocked while the stdout pipe
	// stays open, which is what WaitDelay closes.
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		lastCmdlineProbeErr = probeFailure(psBin, ctx.Err(), err)
		return nil, false
	}
	argv := parsePSCommand(string(out))
	if len(argv) == 0 {
		lastCmdlineProbeErr = psBin + " printed nothing for that pid"
		return nil, false
	}
	// A skipped pid must not leave its failure standing as the run's reason.
	lastCmdlineProbeErr = ""
	return argv, true
}

// probeFailure describes a failed probe. A cancelled context reports the
// budget: the "signal: killed" the kill produces names the symptom and hides
// the cause. stderr carries a refusal's own words, so it is kept.
func probeFailure(bin string, ctxErr, err error) string {
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		return bin + " did not answer within " + cmdlineProbeBudget.String()
	}
	msg := bin + ": " + err.Error()
	var exit *exec.ExitError
	if errors.As(err, &exit) && len(exit.Stderr) > 0 {
		msg += ": " + strings.TrimSpace(string(exit.Stderr))
	}
	return msg
}

// parsePSCommand turns a ps row into the argv shellScript reads. ps prints a
// single string with the shell's own quoting already gone, so splitting it on
// spaces would lose the pipe that decides the classification, and the
// boundaries inside a shell's command string are unrecoverable anyway.
// Whatever follows the flag that takes a script therefore stays whole, in the
// spacing ps printed rather than a rejoined copy of it.
func parsePSCommand(out string) []string {
	line, _, _ := strings.Cut(out, "\n")
	line = strings.TrimRight(line, "\r")
	fields := strings.Fields(line)
	if len(fields) < 3 || !isShell(fields[0]) {
		return fields
	}
	for i, f := range fields[1:] {
		if !takesCommandString(f) || i+2 >= len(fields) {
			continue
		}
		script, ok := afterFields(line, i+2)
		if !ok {
			break
		}
		argv := append([]string{}, fields[:i+2]...)
		return append(argv, script)
	}
	return fields
}

// afterFields returns what follows the leading n whitespace-separated fields
// of line, with no leading space. The fields themselves come from Fields, so
// the walk here counts the same boundaries it did.
func afterFields(line string, n int) (string, bool) {
	rest := line
	for i := 0; i < n; i++ {
		rest = strings.TrimLeft(rest, " \t")
		j := strings.IndexAny(rest, " \t")
		if j < 0 {
			return "", false
		}
		rest = rest[j:]
	}
	return strings.TrimLeft(rest, " \t"), true
}
