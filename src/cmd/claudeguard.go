package cmd

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

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

// agentHarness describes one AI coding agent go-toolchain can run under. Every
// listed agent reads a command's output through its own transcript, so hiding
// that output hides it from the agent — the guard treats them identically.
type agentHarness struct {
	name    string   // shown in the abort message
	envVars []string // markers the agent exports into every child's environment
	procs   []string // process-name prefixes of the agent process itself
	pidVars []string // env vars holding the agent process's own PID
}

// agentHarnesses is the guard's roster. procs are matched as prefixes against
// /proc comm (kernel-truncated to 15 bytes), both to detect the agent in this
// process's ancestry and to recognize the agent as the legitimate reader of our
// stdout pipe — an agent that spawns commands with piped stdout (grok,
// opencode) would otherwise trip the guard on every invocation. An agent whose
// binary is renamed beyond these prefixes and exports no pidVar fails closed.
var agentHarnesses = []agentHarness{
	{name: "Claude", envVars: []string{"CLAUDECODE"}, procs: []string{"claude"}},
	{name: "grok build", envVars: []string{"GROK_AGENT"}, procs: []string{"grok", "xai-grok-pager"}},
	{name: "opencode", envVars: []string{"OPENCODE"}, procs: []string{"opencode"}, pidVars: []string{"OPENCODE_PID"}},
}

// harnessForProcess returns the agent whose process-name prefix matches comm.
func harnessForProcess(comm string) (string, bool) {
	for _, h := range agentHarnesses {
		for _, p := range h.procs {
			if strings.HasPrefix(comm, p) {
				return h.name, true
			}
		}
	}
	return "", false
}

// isHarnessPID reports whether pid is an agent process that named itself in the
// environment (opencode exports OPENCODE_PID). This covers an agent running
// from a JS/other runtime, where the process name is the runtime's rather than
// the agent's.
func isHarnessPID(pid int) bool {
	for _, h := range agentHarnesses {
		for _, v := range h.pidVars {
			if s := os.Getenv(v); s != "" {
				if p, err := strconv.Atoi(s); err == nil && p == pid {
					return true
				}
			}
		}
	}
	return false
}

// runningUnderAgent reports the agent go-toolchain is executing underneath, if
// any. Detection is primarily by process ancestry (see agentProcessAncestor)
// and additionally by the environment markers each agent exports into every
// child. The env fallback keeps the guard working when the process name is
// unavailable (e.g. a non-Linux host where the ancestry walk is a no-op, or a
// renamed launcher).
func runningUnderAgent() (string, bool) {
	if name, ok := agentProcessAncestor(); ok {
		return name, true
	}
	return agentFromEnv()
}

// agentFromEnv returns the agent whose environment marker is set.
func agentFromEnv() (string, bool) {
	for _, h := range agentHarnesses {
		for _, v := range h.envVars {
			if val := os.Getenv(v); val != "" && val != "0" {
				return h.name, true
			}
		}
	}
	return "", false
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

// Indirection seams: agentOutputViolation calls these rather than the
// detectors directly, so a test can drive every branch deterministically
// without a real agent ancestor process or a captured stdout descriptor. They
// are unexported, default to the real implementations, and are reassigned only
// from tests in this package. They are NOT a bypass: there is no environment
// variable, flag, or any other runtime knob that disables the guard.
var (
	runningUnderAgentFn = runningUnderAgent
	inspectStdoutFn     = inspectStdout
)

// agentOutputViolation reports the agent, the offending sink and true when
// go-toolchain is running under an agent with its output captured, redirected,
// or discarded instead of printed where the agent will read it. It is a no-op
// (returns false) only when go-toolchain is not running under an agent. The
// guard is unconditional: there is deliberately no way to opt out of it.
func agentOutputViolation() (string, outputSink, bool) {
	agent, ok := runningUnderAgentFn()
	if !ok {
		return "", outputSink{}, false
	}
	s := inspectStdoutFn()
	if s.kind == sinkVisible {
		return agent, s, false
	}
	return agent, s, true
}

// guardAgainstAgentOutputCapture aborts the process immediately (exit 1) when
// go-toolchain is running under an agent and its output is being hidden —
// piped, redirected to a file, or discarded — instead of shown in the
// transcript. It is a no-op in every other situation.
// agentGuardOut is a deliberate logger bypass: the abort message below MUST
// always reach the real stderr and must never become a stdout GHA annotation,
// because the guard fires precisely when stdout is redirected or captured (the
// smoke-linux CI step asserts the "refused to run" text on stderr). Held in a
// variable, which the bannedoutput analyzer deliberately permits.
var agentGuardOut io.Writer = os.Stderr

func guardAgainstAgentOutputCapture() {
	if agent, s, bad := agentOutputViolation(); bad {
		// Refusing to run is not enough on its own. The invocation that hides
		// the output typically ignores the exit code too, and a binary from an
		// earlier successful run is still sitting at build/<target>, ready to
		// be executed as proof of a build that never happened. Delete it: an
		// aborted run must leave nothing runnable behind (see staleoutputs.go).
		fmt.Fprint(agentGuardOut, agentOutputMessage(agent, s, discardBuildOutputsFromCWD()))
		os.Exit(1)
	}
}

// agentOutputMessage renders the abort message for the given agent and sink,
// listing the build outputs the abort deleted (if any).
func agentOutputMessage(agent string, s outputSink, removed []string) string {
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
	fmt.Fprintf(&b, "You are running under %s, where go-toolchain's FULL output must land in\n", agent)
	b.WriteString("your transcript so you actually read it — the \"Coverage targets\" list, the\n")
	b.WriteString("total-coverage line, and any test or build failures. Capturing it instead —\n")
	b.WriteString("a pipe (head/tail/grep/sed/awk/cat/tee/…), a `> file` or `>> file` redirect,\n")
	b.WriteString("a `$(...)` capture, or `/dev/null` — truncates or hides exactly what matters.\n\n")
	b.WriteString("Run go-toolchain on its own, with nothing after it, and read the whole thing:\n")
	b.WriteString("    go-toolchain\n")
	if len(removed) > 0 {
		b.WriteString("\nThe build outputs of the previous run have been DELETED, so an old binary\n")
		b.WriteString("cannot stand in for a build you did not run:\n")
		for _, path := range removed {
			fmt.Fprintf(&b, "    %s\n", path)
		}
		b.WriteString("Run go-toolchain as shown above to build them again.\n")
	}
	return b.String()
}
