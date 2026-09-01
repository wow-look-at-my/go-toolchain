//go:build linux || cosmo

// The symbols under test (inspectFD, procCommPPID, isAncestorPID,
// pipePeerName, isTerminal) live in claudeguard_proc.go and
// claudeguard_tty_{linux,cosmo}.go, all constrained to `linux || cosmo`. This
// file must carry the same constraint or it fails to compile on darwin and
// windows, where those definitions are absent. claudeguard_buildtags_test.go
// (which pins the classifier's own constraints) skips _test.go files, so it is
// unaffected and still runs everywhere.

package cmd

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	agent "github.com/wow-look-at-my/is-this-an-agent"
	"golang.org/x/sys/unix"
)

// TestMain re-execs this binary as the "child" side of a socketpair (the real
// go-toolchain/opencode topology): SO_PEERCRED only resolves a peer pid
// across a genuine fork/exec, so the socket_* subtests spawn a real process.
func TestMain(m *testing.M) {
	if os.Getenv("CLAUDEGUARD_TEST_HELPER") == "inspect_fd1" {
		s := inspectFD(1)
		result := "HELPER_KIND=" + strconv.Itoa(int(s.kind)) + " HELPER_DETAIL=" + s.detail + "\n"
		if path := os.Getenv("CLAUDEGUARD_TEST_RESULT_FILE"); path != "" {
			// The script tool dup2s stderr onto the same pty as stdout; use a plain file instead.
			_ = os.WriteFile(path, []byte(result), 0o600)
		} else {
			os.Stderr.WriteString(result)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runSocketPeerHelper creates a real AF_UNIX socketpair, re-execs this test
// binary with the far end as its stdout (closing our own copy immediately, like
// opencode/Node do, rather than after Wait — see socketharness.go), and
// returns what the helper's inspectFD classified that fd as.
func runSocketPeerHelper(t *testing.T, extraEnv ...string) (sinkKind, string) {
	t.Helper()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	require.NoError(t, err)
	readerEnd := os.NewFile(uintptr(fds[0]), "reader")
	childStdout := os.NewFile(uintptr(fds[1]), "writer")
	defer readerEnd.Close()

	cmd := exec.Command(os.Args[0])
	cmd.Env = append(append([]string{}, os.Environ()...), append(extraEnv, "CLAUDEGUARD_TEST_HELPER=inspect_fd1")...)
	cmd.Stdout = childStdout
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	require.NoError(t, cmd.Start())
	childStdout.Close()
	require.NoError(t, cmd.Wait())

	out := strings.TrimSpace(errBuf.String())
	rest, ok := strings.CutPrefix(out, "HELPER_KIND=")
	require.True(t, ok, "unexpected helper output: %q", out)
	kindStr, detail, ok := strings.Cut(rest, " HELPER_DETAIL=")
	require.True(t, ok, "unexpected helper output: %q", out)
	k, err := strconv.Atoi(kindStr)
	require.NoError(t, err)
	return sinkKind(k), detail
}

// runScriptWrapperHelper reproduces the reported bypass verbatim:
// `script -qec "<helper>" LOGFILE`. The script tool forkpty()s a fresh pty and
// dup2s it onto the child's stdin/stdout/stderr, so isatty passes inside
// the helper even though script simultaneously writes everything the pty
// produces to LOGFILE byte for byte -- the exact hole ptyWrapperAncestor
// (claudeguard_ptywrap.go) closes. The helper reports what it saw through a
// plain file, never stderr: under script, stderr is the SAME recorded pty as
// stdout, so writing there would land in the log this test is trying to
// prove is a problem.
func runScriptWrapperHelper(t *testing.T, extraEnv ...string) (kind sinkKind, detail, log string) {
	t.Helper()
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script(1) not on PATH")
	}
	dir := t.TempDir()
	logPath := dir + "/typescript.log"
	resultPath := dir + "/result.txt"

	cmd := exec.Command("script", "-qec", os.Args[0], logPath)
	cmd.Env = append(append([]string{}, os.Environ()...), append(extraEnv,
		"CLAUDEGUARD_TEST_HELPER=inspect_fd1",
		"CLAUDEGUARD_TEST_RESULT_FILE="+resultPath,
	)...)
	require.NoError(t, cmd.Run())

	out, err := os.ReadFile(resultPath)
	require.NoError(t, err, "helper under script(1) never wrote a result")
	rest, ok := strings.CutPrefix(strings.TrimSpace(string(out)), "HELPER_KIND=")
	require.True(t, ok, "unexpected helper output: %q", out)
	kindStr, d, ok := strings.Cut(rest, " HELPER_DETAIL=")
	require.True(t, ok, "unexpected helper output: %q", out)
	k, err := strconv.Atoi(kindStr)
	require.NoError(t, err)

	logBytes, _ := os.ReadFile(logPath)
	return sinkKind(k), d, string(logBytes)
}

// TestScriptWrapperCannotFakeATerminal is the reported bypass, run for real:
// `script -qec "go-toolchain" /tmp/gt-full3.log` (here, the test helper
// standing in for go-toolchain). Before ptyWrapperAncestor existed, stdout's
// isatty() check alone would have classified this as sinkVisible -- a real
// terminal -- because a pty slave IS a terminal, regardless of who allocated it.
func TestScriptWrapperCannotFakeATerminal(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("needs /proc (linux)")
	}
	kind, detail, log := runScriptWrapperHelper(t, "CLAUDECODE=1")
	assert.Equal(t, sinkHidden, kind, "a pty script(1) allocated must not read as a real terminal")
	assert.Equal(t, "script", detail)
	assert.Contains(t, log, "Script started", "script(1) must have really run, so this exercised the pty path")
	assert.Contains(t, log, "Script done")
}

func TestAgentOutputMessageVariants(t *testing.T) {
	t.Parallel()
	pipe := agentOutputMessage("Claude", outputSink{kind: sinkPipe, detail: "head"}, nil)
	assert.Contains(t, pipe, "piped into `head`")

	file := agentOutputMessage("Claude", outputSink{kind: sinkFile, detail: "/tmp/x.log"}, nil)
	assert.Contains(t, file, "redirected to the file `/tmp/x.log`")

	discard := agentOutputMessage("Claude", outputSink{kind: sinkDiscard, detail: "/dev/null"}, nil)
	assert.Contains(t, discard, "discarded to `/dev/null`")

	// sinkHidden must still surface the reader's name in the message.
	hiddenWithDetail := agentOutputMessage("opencode", outputSink{kind: sinkHidden, detail: "node"}, nil)
	assert.Contains(t, hiddenWithDetail, "reader: `node`")

	hiddenNoDetail := agentOutputMessage("opencode", outputSink{kind: sinkHidden}, nil)
	assert.Contains(t, hiddenNoDetail, "captured instead of printed to the terminal")
	assert.NotContains(t, hiddenNoDetail, "reader:")

	// The agent that hid the output is named back at it -- every agent the
	// roster knows, so an agent added upstream is covered here without an
	// edit.
	for _, a := range agent.Roster() {
		assert.Contains(t,
			agentOutputMessage(a.Name, outputSink{kind: sinkPipe, detail: "head"}, nil),
			"running under "+a.Name)
	}

	for _, m := range []string{pipe, file, discard} {
		assert.Contains(t, m, "go-toolchain")
		assert.Contains(t, m, "with nothing after it")
		// The fix is to run it plainly — never to redirect to a file.
		assert.NotContains(t, m, "write the full output to a file")
		// Nothing was deleted, so the message must not claim otherwise.
		assert.NotContains(t, m, "DELETED")
	}

	// When the abort deleted the previous run's binaries, it says so and names
	// them — otherwise the missing build/<target> looks like a different bug.
	deleted := agentOutputMessage("Claude", outputSink{kind: sinkPipe, detail: "cat"},
		[]string{"/repo/build/mytool", "/repo/build/mytool_linux_amd64"})
	assert.Contains(t, deleted, "DELETED")
	assert.Contains(t, deleted, "/repo/build/mytool")
	assert.Contains(t, deleted, "/repo/build/mytool_linux_amd64")
}

// TestAgentOutputMessageRendersTheWholeDocument pins the refusal message byte
// for byte, with the deleted-outputs block and without it. The Contains
// assertions above pass on a message whose blank lines have moved, and this
// message is the only thing the aborted run prints.
func TestAgentOutputMessageRendersTheWholeDocument(t *testing.T) {
	t.Parallel()
	const body = "\n" +
		"You are running under Claude, where go-toolchain's FULL output must land in\n" +
		"your transcript so you actually read it — the \"Coverage targets\" list, the\n" +
		"total-coverage line, and any test or build failures. Capturing it instead —\n" +
		"a pipe (head/tail/grep/sed/awk/cat/tee/…), a `> file` or `>> file` redirect,\n" +
		"a `$(...)` capture, or `/dev/null` — truncates or hides exactly what matters.\n" +
		"\n" +
		"Run go-toolchain on its own, with nothing after it, and read the whole thing:\n" +
		"    go-toolchain\n"
	head := "\n" + colorBoldRed + "✗ go-toolchain refused to run: its output is being piped into `head`." +
		colorReset + "\n"

	sink := outputSink{kind: sinkPipe, detail: "head"}
	assert.Equal(t, head+body, agentOutputMessage("Claude", sink, nil))
	assert.Equal(t, head+body+
		"\n"+
		"The build outputs of the previous run have been DELETED, so an old binary\n"+
		"cannot stand in for a build you did not run:\n"+
		"    /repo/build/mytool\n"+
		"    /repo/build/mytool_linux_amd64\n"+
		"Run go-toolchain as shown above to build them again.\n",
		agentOutputMessage("Claude", sink, []string{"/repo/build/mytool", "/repo/build/mytool_linux_amd64"}))
}

func TestAgentOutputViolation(t *testing.T) {
	origUnder, origSink := runningUnderAgentFn, inspectStdoutFn
	t.Cleanup(func() { runningUnderAgentFn, inspectStdoutFn = origUnder, origSink })

	// Not running under an agent: never a violation, whatever the sink.
	runningUnderAgentFn = func() (string, bool) { return "", false }
	inspectStdoutFn = func() outputSink { return outputSink{kind: sinkPipe, detail: "head"} }
	_, _, bad := agentOutputViolation()
	assert.False(t, bad, "no violation when not running under an agent")

	// Visible output isn't a violation; the exiting wrapper must no-op here or it kills the test.
	runningUnderAgentFn = func() (string, bool) { return "Claude", true }
	inspectStdoutFn = func() outputSink { return outputSink{kind: sinkVisible} }
	_, _, bad = agentOutputViolation()
	assert.False(t, bad, "visible output is not a violation")
	guardAgainstAgentOutputCapture()

	// Hidden output violates for every agent on the roster; no env var or flag turns this off.
	inspectStdoutFn = func() outputSink { return outputSink{kind: sinkPipe, detail: "head"} }
	for _, a := range agent.Roster() {
		runningUnderAgentFn = func() (string, bool) { return a.Name, true }
		name, s, bad := agentOutputViolation()
		assert.True(t, bad, "captured output under %s is a violation", a.Name)
		assert.Equal(t, a.Name, name)
		assert.Equal(t, sinkPipe, s.kind)
		assert.Equal(t, "head", s.detail)
	}
}

func TestDetectAgentNamesTheAgent(t *testing.T) {
	// go-toolchain's adapter over the library: whatever the roster answers,
	// the guard needs the agent's NAME for its message.
	for _, v := range agent.Roster()[0].EnvVars {
		t.Setenv(v, "")
	}
	if _, underAgent := agent.ProcessAncestor(); underAgent {
		t.Skip("this test process is under an agent; ancestry answers before the marker")
	}
	name, ok := detectAgent()
	assert.False(t, ok, "no agent, no name")
	assert.Empty(t, name)

	t.Setenv(agent.Roster()[0].EnvVars[0], "1")
	name, ok = detectAgent()
	require.True(t, ok)
	assert.Equal(t, agent.Roster()[0].Name, name)
}

func TestPipePeerNameDetectsConsumer(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("pipePeerName needs /proc (linux)")
	}
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep not on PATH")
	}
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer w.Close()

	cmd := exec.Command("sleep", "30")
	cmd.Stdin = r // child inherits the read end: same pipe inode as w
	require.NoError(t, cmd.Start())
	r.Close()
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	target, err := os.Readlink("/proc/self/fd/" + strconv.FormatUint(uint64(w.Fd()), 10))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(target, "pipe:"), "fd should be a pipe, got %q", target)

	name, pid, ok := pipePeerName(target)
	require.True(t, ok, "expected to identify the pipe consumer")
	assert.Equal(t, "sleep", name)
	assert.Equal(t, cmd.Process.Pid, pid)
}

// TestPipePeerNameSkipsAWriteEndSibling reproduces the grok-build false
// positive: the shell that forks a command keeps its own stdout fd open
// while the command runs as its child, and that fd resolves to the exact
// same "pipe:[ino]" string as the write end being inspected. A scan that
// matches on the string alone can return that shell instead of the real
// reader, and refuse a run that was never piped anywhere.
func TestPipePeerNameSkipsAWriteEndSibling(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("pipePeerName needs /proc (linux)")
	}
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep not on PATH")
	}
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not on PATH")
	}

	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer w.Close()

	// Holds the read end, the way an agent's harness reads a tool call's stdout.
	reader := exec.Command("sleep", "30")
	reader.Stdin = r
	require.NoError(t, reader.Start())
	r.Close()
	defer func() { _ = reader.Process.Kill(); _ = reader.Wait() }()

	// Another holder of the write end, standing in for the shell.
	siblingStdinR, siblingStdinW, err := os.Pipe()
	require.NoError(t, err)
	defer siblingStdinW.Close()
	sibling := exec.Command("cat")
	sibling.Stdin = siblingStdinR
	sibling.Stdout = w
	require.NoError(t, sibling.Start())
	siblingStdinR.Close()
	defer func() { _ = sibling.Process.Kill(); _ = sibling.Wait() }()

	target, err := os.Readlink("/proc/self/fd/" + strconv.FormatUint(uint64(w.Fd()), 10))
	require.NoError(t, err)

	name, pid, ok := pipePeerName(target)
	require.True(t, ok, "expected to identify the read-end consumer past the write-end sibling")
	assert.Equal(t, "sleep", name)
	assert.Equal(t, reader.Process.Pid, pid, "must not return the write-end sibling's pid")
}

func TestPipeReaderAllowanceThroughTheGuard(t *testing.T) {
	t.Parallel()
	// Pins the classifier's rule: an agent reading our pipe counts as capture; a filter does not.
	if runtime.GOOS != "linux" {
		t.Skip("needs /proc (linux)")
	}
	parent := os.Getppid()

	assert.True(t, agent.IsPipeReader("opencode", parent))
	assert.False(t, agent.IsPipeReader("head", parent), "a filter is not the harness")
	assert.False(t, agent.IsPipeReader("opencode", os.Getpid()), "self is not an ancestor")
}

func TestInspectFDClassification(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("inspectFD needs /proc (linux)")
	}

	t.Run("pipe_to_filter_is_blocked", func(t *testing.T) {
		if _, err := exec.LookPath("sleep"); err != nil {
			t.Skip("sleep not on PATH")
		}
		r, w, err := os.Pipe()
		require.NoError(t, err)
		defer w.Close()
		cmd := exec.Command("sleep", "30")
		cmd.Stdin = r
		require.NoError(t, cmd.Start())
		r.Close()
		defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

		s := inspectFD(w.Fd())
		assert.Equal(t, sinkPipe, s.kind)
		assert.Equal(t, "sleep", s.detail)
	})

	t.Run("plain_file_is_blocked", func(t *testing.T) {
		t.Setenv("CLAUDE_CODE_SESSION_ID", "SID-unit-test")
		f, err := os.CreateTemp(t.TempDir(), "out-*.log")
		require.NoError(t, err)
		defer f.Close()
		s := inspectFD(f.Fd())
		assert.Equal(t, sinkFile, s.kind)
		assert.Contains(t, s.detail, ".log")
	})

	t.Run("harness_capture_file_is_allowed", func(t *testing.T) {
		t.Setenv("CLAUDE_CODE_SESSION_ID", "SID-unit-test")
		// A path embedding the session id is the harness's transcript capture, the only redirect that doesn't hide output.
		f, err := os.CreateTemp(t.TempDir(), "SID-unit-test-*.output")
		require.NoError(t, err)
		defer f.Close()
		s := inspectFD(f.Fd())
		assert.Equal(t, sinkVisible, s.kind)
	})

	t.Run("dev_null_is_blocked", func(t *testing.T) {
		f, err := os.OpenFile("/dev/null", os.O_WRONLY, 0)
		require.NoError(t, err)
		defer f.Close()
		s := inspectFD(f.Fd())
		assert.Equal(t, sinkDiscard, s.kind)
	})

	// A socket looked hardcoded-hidden before, with no attempt at peer
	// identification at all -- this is what an agent's own tool-execution
	// plumbing actually is (a socketpair for a child's stdio, not a bare
	// pipe; see claudeguard_darwin.go's file header for why that matters on
	// darwin too), so it now gets the exact same chance a pipe gets.
	t.Run("socket_reader_that_is_not_an_agent_is_blocked", func(t *testing.T) {
		// The SO_PEERCRED peer is this binary's own parent, unnamed and unclaimed by any PID var, so the guard must refuse.
		kind, detail := runSocketPeerHelper(t)
		assert.Equal(t, sinkHidden, kind)
		assert.NotEmpty(t, detail, "refusal message must name the unrecognized reader, not go silent")
	})

	t.Run("socket_reader_recognized_via_pid_var_is_allowed", func(t *testing.T) {
		// opencode/Node case: the SO_PEERCRED parent names its own pid via OPENCODE_PID in the child's env.
		kind, _ := runSocketPeerHelper(t, "OPENCODE=1", "OPENCODE_PID="+strconv.Itoa(os.Getpid()))
		assert.Equal(t, sinkVisible, kind)
	})

	t.Run("socket_reader_with_wrong_pid_var_is_still_blocked", func(t *testing.T) {
		// A PID var naming another pid must not fool the guard; SO_PEERCRED is the kernel's own record.
		kind, _ := runSocketPeerHelper(t, "OPENCODE=1", "OPENCODE_PID=1")
		assert.Equal(t, sinkHidden, kind)
	})

	t.Run("socket_reader_recognized_via_grok_pid_var_is_allowed", func(t *testing.T) {
		kind, _ := runSocketPeerHelper(t, "GROK_AGENT=1", grokPIDEnv+"="+strconv.Itoa(os.Getpid()))
		assert.Equal(t, sinkVisible, kind)
	})

	t.Run("socket_reader_with_wrong_grok_pid_var_is_still_blocked", func(t *testing.T) {
		kind, _ := runSocketPeerHelper(t, "GROK_AGENT=1", grokPIDEnv+"=1")
		assert.Equal(t, sinkHidden, kind)
	})
}

func TestIsTerminalOnPipeIsFalse(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("isTerminal needs unix termios")
	}
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer r.Close()
	defer w.Close()
	assert.False(t, isTerminal(w.Fd()), "a pipe is not a terminal")
}

// TestInspectStdoutIgnoresStdoutVariableReassignment guards against
// inspectStdout using os.Stdout.Fd() instead of the raw descriptor: logx.Install()
// reassigns that variable, so following it would misclassify a real
// terminal or capture file as a hidden sink under every agent.
func TestInspectStdoutIgnoresStdoutVariableReassignment(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("inspectStdout needs /proc (linux)")
	}
	origStdout := os.Stdout
	f, err := os.CreateTemp(t.TempDir(), "decoy-stdout-*.log")
	require.NoError(t, err)
	os.Stdout = f
	t.Cleanup(func() {
		os.Stdout = origStdout
		_ = f.Close()
	})

	s := inspectStdout()
	assert.NotEqual(t, sinkFile, s.kind, "must not classify by the decoy os.Stdout file")
	assert.NotContains(t, s.detail, "decoy-stdout",
		"inspectStdout must inspect the real fd 1, not whatever os.Stdout currently points to")
}
