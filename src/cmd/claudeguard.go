package cmd

import (
	"fmt"
	"os"
	"strings"
)

// allowPipeFilterEnv, when set to a non-empty value, disables the Claude
// pipe-filter guard below. It exists as an operator/CI escape hatch and to make
// the guard testable. It is deliberately NOT mentioned in the abort message:
// the guard's whole purpose is to push the agent toward reading the full output
// rather than quietly working around the check.
const allowPipeFilterEnv = "GO_TOOLCHAIN_ALLOW_PIPE_FILTER"

// filterCommands are output-mangling "filter" programs. When go-toolchain runs
// under the Claude agent and its stdout is piped straight into one of these,
// the agent is truncating or hiding the build output — the "Coverage targets"
// list, the total-coverage line, and any test/build failures, i.e. the exact
// information it needs to act on. go-toolchain refuses to run in that case.
//
// Keys are process comm values (the executable's base name as reported by
// /proc/<pid>/comm, truncated by the kernel to 15 bytes). Pagers (less/more)
// are included because piping into them under Claude hangs forever, which is no
// better than truncation; the guard turns that hang into an actionable error.
var filterCommands = map[string]bool{
	"head":  true,
	"tail":  true,
	"grep":  true,
	"egrep": true,
	"fgrep": true,
	"rg":    true, // ripgrep
	"ag":    true, // the silver searcher
	"sed":   true,
	"awk":   true,
	"gawk":  true,
	"mawk":  true,
	"cut":   true,
	"wc":    true,
	"tac":   true,
	"uniq":  true,
	"less":  true,
	"more":  true,
}

// runningUnderClaude reports whether go-toolchain is executing underneath the
// Claude agent. Detection is primarily by process ancestry — an ancestor whose
// process name begins with "claude" (see claudeProcessAncestor) — and
// additionally by the CLAUDECODE marker the Claude Code CLI exports into the
// environment of every child. The env fallback keeps the guard working when the
// process name is unavailable (e.g. a non-Linux host where the ancestry walk is
// a no-op, or a renamed launcher).
func runningUnderClaude() bool {
	if claudeProcessAncestor() {
		return true
	}
	if v := os.Getenv("CLAUDECODE"); v != "" && v != "0" {
		return true
	}
	return false
}

// stdoutFilterConsumer returns the name of the filter program reading
// go-toolchain's stdout, and true, when stdout is piped directly into one of
// filterCommands. It returns ("", false) otherwise — including when stdout is a
// terminal, a regular file (e.g. `go-toolchain > out.log`), or a pipe whose
// consumer is not a recognized filter.
func stdoutFilterConsumer() (string, bool) {
	peer, ok := stdoutPipePeerName()
	if !ok || !filterCommands[peer] {
		return "", false
	}
	return peer, true
}

// claudePipeFilterViolation reports the offending filter command and true when
// all of the following hold: the guard is not disabled, go-toolchain is running
// under Claude, and stdout is piped into a recognized filter.
func claudePipeFilterViolation() (string, bool) {
	if os.Getenv(allowPipeFilterEnv) != "" {
		return "", false
	}
	if !runningUnderClaude() {
		return "", false
	}
	return stdoutFilterConsumer()
}

// guardAgainstClaudePipeFilter aborts the process immediately (exit 1) when
// go-toolchain is running under Claude with its output piped into a filter such
// as head/tail/grep/sed/awk. It is a no-op in every other situation.
func guardAgainstClaudePipeFilter() {
	if peer, bad := claudePipeFilterViolation(); bad {
		fmt.Fprint(os.Stderr, claudePipeFilterMessage(peer))
		os.Exit(1)
	}
}

// claudePipeFilterMessage renders the abort message for a stdout pipe into the
// named filter program.
func claudePipeFilterMessage(peer string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s✗ go-toolchain refused to run: its output is being piped into `%s`.%s\n\n", colorBoldRed, peer, colorReset)
	b.WriteString("You are running under Claude and filtering go-toolchain's output. Filters like\n")
	b.WriteString("head/tail/grep/sed/awk truncate or hide the parts that matter most — the\n")
	b.WriteString("\"Coverage targets\" list, the total-coverage line, and any test/build failures.\n\n")
	b.WriteString("Re-run go-toolchain with NO pipe and read the ENTIRE output:\n")
	b.WriteString("    go-toolchain\n\n")
	b.WriteString("If you must search it, write the full output to a file first, then read the file:\n")
	b.WriteString("    go-toolchain > /tmp/go-toolchain.log 2>&1\n")
	b.WriteString("    # then open /tmp/go-toolchain.log and read all of it\n")
	return b.String()
}
