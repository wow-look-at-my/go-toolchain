package cmd

import (
	"fmt"
	"os"
	"strings"
)

// allowGuardEnv, when set to a non-empty value, disables the Claude output
// guard below. It is an operator/CI escape hatch and a test seam; it is
// deliberately NOT mentioned in the abort message, so the guard's default
// behavior is to force the full output into view rather than offer a bypass.
const allowGuardEnv = "GO_TOOLCHAIN_ALLOW_OUTPUT_CAPTURE"

// sinkKind classifies where go-toolchain's stdout is going.
type sinkKind int

const (
	sinkVisible sinkKind = iota // terminal or the harness's own capture — full output is shown
	sinkPipe                    // piped into another process (| head, | cat, $(...), ...)
	sinkFile                    // redirected to a non-capture file (> out.log)
	sinkDiscard                 // sent to /dev/null or a non-terminal device
	sinkHidden                  // socket/anon-inode/other: not visible
)

// outputSink describes go-toolchain's stdout after inspection.
type outputSink struct {
	kind   sinkKind
	detail string // peer command name (pipe) or path (file/discard)
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

// isHarnessCapturePath reports whether path is the Claude Code harness's own
// per-task stdout capture file — the file the Bash tool redirects a command's
// stdout to and streams verbatim into the transcript. That is the ONE redirect
// that does not hide output (it IS how the agent sees it), so it must be
// allowed. Every agent-introduced redirect (`> out.log`, `> /dev/null`, …)
// targets something else and is refused.
//
// The capture path embeds this session's id (a UUID) and ends in ".output"
// under a ".../tasks/" directory, e.g.
// /tmp/claude-0/-home-user/<CLAUDE_CODE_SESSION_ID>/tasks/<id>.output. Matching
// the session id is the strong signal; the ".output"+"claude" structural match
// is a fallback so a minor change to the harness path scheme cannot wedge the
// guard into blocking every normal run.
func isHarnessCapturePath(path string) bool {
	if sid := os.Getenv("CLAUDE_CODE_SESSION_ID"); sid != "" && strings.Contains(path, sid) {
		return true
	}
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".output") && strings.Contains(lower, "claude")
}

// claudeOutputViolation reports the offending sink and true when go-toolchain is
// running under Claude with its output captured, redirected, or discarded
// instead of printed where the agent will read it. It is a no-op (returns
// false) when the guard is disabled or go-toolchain is not running under Claude.
func claudeOutputViolation() (outputSink, bool) {
	if os.Getenv(allowGuardEnv) != "" {
		return outputSink{}, false
	}
	if !runningUnderClaude() {
		return outputSink{}, false
	}
	s := inspectStdout()
	if s.kind == sinkVisible {
		return s, false
	}
	return s, true
}

// guardAgainstClaudeOutputCapture aborts the process immediately (exit 1) when
// go-toolchain is running under Claude and its output is being hidden — piped,
// redirected to a file, or discarded — instead of shown in the transcript. It
// is a no-op in every other situation.
func guardAgainstClaudeOutputCapture() {
	if s, bad := claudeOutputViolation(); bad {
		fmt.Fprint(os.Stderr, claudeOutputMessage(s))
		os.Exit(1)
	}
}

// claudeOutputMessage renders the abort message for the given sink.
func claudeOutputMessage(s outputSink) string {
	var what string
	switch s.kind {
	case sinkPipe:
		if s.detail != "" {
			what = fmt.Sprintf("piped into `%s`", s.detail)
		} else {
			what = "piped into another command"
		}
	case sinkFile:
		what = fmt.Sprintf("redirected to the file `%s`", s.detail)
	case sinkDiscard:
		what = fmt.Sprintf("discarded to `%s`", s.detail)
	default:
		what = "captured instead of printed to the terminal"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\n%s✗ go-toolchain refused to run: its output is being %s.%s\n\n", colorBoldRed, what, colorReset)
	b.WriteString("You are running under Claude, where go-toolchain's FULL output must land in\n")
	b.WriteString("your transcript so you actually read it — the \"Coverage targets\" list, the\n")
	b.WriteString("total-coverage line, and any test or build failures. Capturing it instead —\n")
	b.WriteString("a pipe (head/tail/grep/sed/awk/cat/tee/…), a `> file` or `>> file` redirect,\n")
	b.WriteString("a `$(...)` capture, or `/dev/null` — truncates or hides exactly what matters.\n\n")
	b.WriteString("Run go-toolchain on its own, with nothing after it, and read the whole thing:\n")
	b.WriteString("    go-toolchain\n")
	return b.String()
}
