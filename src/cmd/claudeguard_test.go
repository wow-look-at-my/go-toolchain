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
)

func TestIsHarnessCapturePath(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "SID-abc-123")
	// Path embedding this session's id is the harness capture — allowed.
	assert.True(t, isHarnessCapturePath("/tmp/claude-0/-home-user/SID-abc-123/tasks/q1.output"))
	// Structural fallback: ends in .output under a path mentioning claude.
	assert.True(t, isHarnessCapturePath("/tmp/claude-0/other/tasks/q1.output"))
	// Ordinary agent redirects are NOT the capture file.
	assert.False(t, isHarnessCapturePath("/tmp/gt.log"))
	assert.False(t, isHarnessCapturePath("/home/user/out.txt"))

	// With no session id, only the structural fallback applies.
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	assert.True(t, isHarnessCapturePath("/var/run/claude/tasks/x.output"))
	assert.False(t, isHarnessCapturePath("/home/user/build.output")) // .output but no "claude"
	assert.False(t, isHarnessCapturePath("/tmp/SID-abc-123/x.log"))
}

func TestAgentOutputMessageVariants(t *testing.T) {
	pipe := agentOutputMessage("Claude", outputSink{kind: sinkPipe, detail: "head"}, nil)
	assert.Contains(t, pipe, "piped into `head`")

	file := agentOutputMessage("Claude", outputSink{kind: sinkFile, detail: "/tmp/x.log"}, nil)
	assert.Contains(t, file, "redirected to the file `/tmp/x.log`")

	discard := agentOutputMessage("Claude", outputSink{kind: sinkDiscard, detail: "/dev/null"}, nil)
	assert.Contains(t, discard, "discarded to `/dev/null`")

	// The agent that hid the output is named back at it.
	for _, agent := range []string{"Claude", "grok build", "opencode"} {
		assert.Contains(t,
			agentOutputMessage(agent, outputSink{kind: sinkPipe, detail: "head"}, nil),
			"running under "+agent)
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
	for _, h := range agentHarnesses {
		runningUnderAgentFn = func() (string, bool) { return h.name, true }
		agent, s, bad := agentOutputViolation()
		assert.True(t, bad, "captured output under %s is a violation", h.name)
		assert.Equal(t, h.name, agent)
		assert.Equal(t, sinkPipe, s.kind)
		assert.Equal(t, "head", s.detail)
	}
}

func TestAgentFromEnvMarkers(t *testing.T) {
	// Every roster agent's marker must be detected on its own. (Tested through
	// agentFromEnv rather than runningUnderAgent so the result does not depend
	// on which agent, if any, is running the test suite.)
	for _, h := range agentHarnesses {
		for _, v := range h.envVars {
			t.Run(v, func(t *testing.T) {
				for _, other := range agentHarnesses {
					for _, ov := range other.envVars {
						t.Setenv(ov, "")
					}
				}
				t.Setenv(v, "1")
				name, ok := agentFromEnv()
				require.True(t, ok, "%s=1 must be detected", v)
				assert.Equal(t, h.name, name)

				// "0" and empty are not markers.
				t.Setenv(v, "0")
				_, ok = agentFromEnv()
				assert.False(t, ok, "%s=0 must not be detected", v)
			})
		}
	}
}

func TestHarnessForProcess(t *testing.T) {
	for name, comm := range map[string]string{
		"Claude":     "claude",
		"grok build": "grok",
		"opencode":   "opencode",
	} {
		got, ok := harnessForProcess(comm)
		require.True(t, ok, "%q must identify an agent", comm)
		assert.Equal(t, name, got)
	}
	// The grok binary also ships under its build artifact name.
	got, ok := harnessForProcess("xai-grok-pager")
	require.True(t, ok)
	assert.Equal(t, "grok build", got)

	_, ok = harnessForProcess("bash")
	assert.False(t, ok, "a shell is not an agent")
	_, ok = harnessForProcess("head")
	assert.False(t, ok, "a filter is not an agent")
}

func TestIsHarnessPID(t *testing.T) {
	t.Setenv("OPENCODE_PID", strconv.Itoa(os.Getpid()))
	assert.True(t, isHarnessPID(os.Getpid()), "the pid opencode exported is the agent")
	assert.False(t, isHarnessPID(os.Getpid()+1))

	t.Setenv("OPENCODE_PID", "")
	assert.False(t, isHarnessPID(os.Getpid()))
}

func TestProcCommPPIDSelf(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("procCommPPID needs /proc (linux)")
	}
	comm, ppid, ok := procCommPPID(os.Getpid())
	require.True(t, ok)
	assert.NotEmpty(t, comm)
	assert.Greater(t, ppid, 0)

	_, _, ok = procCommPPID(0) // PID 0 has no /proc entry
	assert.False(t, ok)
}

func TestIsAncestorPID(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("isAncestorPID needs /proc (linux)")
	}
	assert.True(t, isAncestorPID(os.Getppid()), "direct parent is an ancestor")
	assert.False(t, isAncestorPID(os.Getpid()), "self is not its own ancestor")
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

func TestIsHarnessPipeReader(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("isHarnessPipeReader needs /proc (linux)")
	}
	parent := os.Getppid()

	// An agent-named ancestor reading our stdout is the harness capturing it —
	// the normal path under grok and opencode, which always pipe.
	assert.True(t, isHarnessPipeReader("opencode", parent))
	assert.True(t, isHarnessPipeReader("grok", parent))

	// A shell (a `$(...)` capture) or a filter is never the harness, even when
	// it is an ancestor.
	assert.False(t, isHarnessPipeReader("bash", parent))
	assert.False(t, isHarnessPipeReader("head", parent))

	// An agent running from a JS runtime is recognized by the pid it exports.
	t.Setenv("OPENCODE_PID", strconv.Itoa(parent))
	assert.True(t, isHarnessPipeReader("bun", parent))
	// ...but only for a process that is actually an ancestor.
	assert.False(t, isHarnessPipeReader("opencode", os.Getpid()), "self is not an ancestor")
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
