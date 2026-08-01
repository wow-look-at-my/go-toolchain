package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	agent "github.com/wow-look-at-my/is-this-an-agent"
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

// Which agents exist, what they look like, and how to recognize one from a
// process tree live in github.com/wow-look-at-my/is-this-an-agent. Keeping
// that roster here meant every other tool needing the same answer wrote its
// own -- and go-toolchain's stopped at the agents it happened to know.
//
// What stays here is the part that is go-toolchain's own: classifying where
// stdout went, and refusing to run when the answer means the agent will never
// read it.

// Indirection seams: agentOutputViolation calls these rather than the
// detectors directly, so a test can drive every branch deterministically
// without a real agent ancestor process or a captured stdout descriptor. They
// are unexported, default to the real implementations, and are reassigned only
// from tests in this package. They are NOT a bypass: there is no environment
// variable, flag, or any other runtime knob that disables the guard.
var (
	runningUnderAgentFn = detectAgent
	inspectStdoutFn     = inspectStdout
)

// detectAgent names the agent go-toolchain is running under, if any:
// ancestry first, then the environment markers each agent exports.
func detectAgent() (string, bool) {
	a, ok := agent.Detect()
	return a.Name, ok
}

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
