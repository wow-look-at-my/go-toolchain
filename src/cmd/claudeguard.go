package cmd

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/template"

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

// The agent roster lives in is-this-an-agent; this file classifies where
// stdout went and refuses to run when the agent will never see it.

// Indirection seams so tests can drive every branch without a real agent.
// NOT a bypass -- no env var or flag disables the guard.
var (
	runningUnderAgentFn = detectAgent
	inspectStdoutFn     = inspectStdout
)

// detectAgent names the agent go-toolchain is running under, if any:
// ancestry, then the environment markers each agent exports.
func detectAgent() (string, bool) {
	a, ok := agent.Detect()
	return a.Name, ok
}

// grokPIDEnv is the pid var grok-build does not export; tests can set it like OPENCODE_PID.
const grokPIDEnv = "GROK_AGENT_PID"

// grokNamedPID reports whether pid is the grok-build process named in
// GROK_AGENT_PID, but only when GROK_AGENT itself is set — the pid var
// without the marker is not a grok session.
func grokNamedPID(pid int) bool {
	if v := os.Getenv("GROK_AGENT"); v == "" || v == "0" {
		return false
	}
	s := os.Getenv(grokPIDEnv)
	if s == "" {
		return false
	}
	p, err := strconv.Atoi(s)
	return err == nil && p == pid
}

// harnessIsPID is agent.IsPID plus GROK_AGENT_PID, the seam the darwin
// socket/FIFO tests need since the library has no grok pid var.
func harnessIsPID(pid int) bool {
	return agent.IsPID(pid) || grokNamedPID(pid)
}

// harnessIsPipeReader is agent.IsPipeReader plus GROK_AGENT_PID, still
// requiring the named pid to be an ancestor so a sibling `| cat` cannot
// borrow the var.
func harnessIsPipeReader(comm string, pid int) bool {
	if agent.IsPipeReader(comm, pid) {
		return true
	}
	return grokNamedPID(pid) && agent.IsAncestorPID(pid)
}

// agentOutputViolation reports the agent, the sink, and true when its output
// is captured, redirected, or discarded instead of printed. False only when
// not running under an agent. Unconditional: no way to opt out.
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

// agentGuardOut bypasses the logger: the abort message must reach real stderr, never a GHA annotation.
var agentGuardOut io.Writer = os.Stderr

func guardAgainstAgentOutputCapture() {
	if agent, s, bad := agentOutputViolation(); bad {
		// Delete stale build outputs too: a caller that hides output often ignores the exit code (see staleoutputs.go).
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
	case sinkHidden:
		if s.detail != "" {
			what = fmt.Sprintf("captured instead of printed to the terminal (reader: `%s`)", s.detail)
		} else {
			what = "captured instead of printed to the terminal"
		}
	default:
		what = "captured instead of printed to the terminal"
	}

	var b strings.Builder
	err := agentOutputTemplate.Execute(&b, struct {
		Red, Reset, What, Agent string
		Removed                 []string
	}{colorBoldRed, colorReset, what, agent, removed})
	if err != nil {
		// This message is the whole reason the run aborts. A caller that
		// printed nothing would look like a crash with no cause.
		return fmt.Sprintf("\ngo-toolchain refused to run: its output is being %s.\n"+
			"(the full message failed to render: %v)\n", what, err)
	}
	return b.String()
}

// agentOutputTemplate is the abort message, held as text. The literals are
// interpreted strings, not a raw string, because the message quotes shell
// with backticks.
var agentOutputTemplate = template.Must(template.New("agent-output").Parse(
	"\n{{.Red}}✗ go-toolchain refused to run: its output is being {{.What}}.{{.Reset}}\n" +
		"\n" +
		"You are running under {{.Agent}}, where go-toolchain's FULL output must land in\n" +
		"your transcript so you actually read it — the \"Coverage targets\" list, the\n" +
		"total-coverage line, and any test or build failures. Capturing it instead —\n" +
		"a pipe (head/tail/grep/sed/awk/cat/tee/…), a `> file` or `>> file` redirect,\n" +
		"a `$(...)` capture, or `/dev/null` — truncates or hides exactly what matters.\n" +
		"\n" +
		"Run go-toolchain on its own, with nothing after it, and read the whole thing:\n" +
		"    go-toolchain\n" +
		"{{if .Removed}}\n" +
		"The build outputs of the previous run have been DELETED, so an old binary\n" +
		"cannot stand in for a build you did not run:\n" +
		"{{range .Removed}}    {{.}}\n" +
		"{{end}}" +
		"Run go-toolchain as shown above to build them again.\n" +
		"{{end}}"))
