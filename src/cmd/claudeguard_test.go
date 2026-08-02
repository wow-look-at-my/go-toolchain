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
)

func TestAgentOutputMessageVariants(t *testing.T) {
	pipe := agentOutputMessage("Claude", outputSink{kind: sinkPipe, detail: "head"}, nil)
	assert.Contains(t, pipe, "piped into `head`")

	file := agentOutputMessage("Claude", outputSink{kind: sinkFile, detail: "/tmp/x.log"}, nil)
	assert.Contains(t, file, "redirected to the file `/tmp/x.log`")

	discard := agentOutputMessage("Claude", outputSink{kind: sinkDiscard, detail: "/dev/null"}, nil)
	assert.Contains(t, discard, "discarded to `/dev/null`")

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

func TestAgentOutputViolation(t *testing.T) {
	origUnder, origSink := runningUnderAgentFn, inspectStdoutFn
	t.Cleanup(func() { runningUnderAgentFn, inspectStdoutFn = origUnder, origSink })

	// Not running under an agent: never a violation, whatever the sink.
	runningUnderAgentFn = func() (string, bool) { return "", false }
	inspectStdoutFn = func() outputSink { return outputSink{kind: sinkPipe, detail: "head"} }
	_, _, bad := agentOutputViolation()
	assert.False(t, bad, "no violation when not running under an agent")

	// Under an agent with visible output (a terminal or the harness capture):
	// allowed. The exiting wrapper must be a no-op on this path — it would
	// os.Exit the test process otherwise.
	runningUnderAgentFn = func() (string, bool) { return "Claude", true }
	inspectStdoutFn = func() outputSink { return outputSink{kind: sinkVisible} }
	_, _, bad = agentOutputViolation()
	assert.False(t, bad, "visible output is not a violation")
	guardAgainstAgentOutputCapture()

	// Hidden output is a violation under EVERY agent on the roster, reported
	// with the agent's name and its sink. There is no env var or flag that can
	// turn this off.
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

func TestPipeReaderAllowanceThroughTheGuard(t *testing.T) {
	// The allowance the sink classifier depends on: an agent reading our pipe
	// is the agent capturing our output, a filter is not. The rule itself is
	// the library's; this pins that the guard still gets the answer it needs.
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
		// A file whose path embeds the session id is the harness's transcript
		// capture — the one redirect that does not hide output.
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
}

func TestIsTerminalOnPipeIsFalse(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("isTerminal needs unix termios")
	}
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer r.Close()
	defer w.Close()
	assert.False(t, isTerminal(w.Fd()), "a pipe is not a terminal")
}

// TestInspectStdoutIgnoresStdoutVariableReassignment guards against a real
// regression: logx.Install() (see src/logx) reassigns the os.Stdout variable
// to its own internal pipe very early in main(), before Cobra -- and thus the
// guard -- ever runs. If inspectStdout classified by os.Stdout.Fd() instead
// of the real descriptor 1, it would see logx's drain pipe instead, whose
// reader is a goroutine in THIS SAME process and therefore invisible to
// pipePeerName's cross-process /proc scan -- misclassifying it as a hidden,
// peer-less sinkPipe and refusing to run under every real agent, even when
// the shell's actual fd 1 is a terminal or the harness's own capture file.
//
// This swaps os.Stdout to a decoy temp file with a distinctive name and
// asserts inspectStdout's result carries no trace of it -- which would only
// be possible if inspectStdout inspects fd 1 directly rather than following
// the (potentially reassigned) os.Stdout variable.
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
