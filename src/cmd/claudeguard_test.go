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

func TestClaudeOutputMessageVariants(t *testing.T) {
	pipe := claudeOutputMessage(outputSink{kind: sinkPipe, detail: "head"}, nil)
	assert.Contains(t, pipe, "piped into `head`")

	file := claudeOutputMessage(outputSink{kind: sinkFile, detail: "/tmp/x.log"}, nil)
	assert.Contains(t, file, "redirected to the file `/tmp/x.log`")

	discard := claudeOutputMessage(outputSink{kind: sinkDiscard, detail: "/dev/null"}, nil)
	assert.Contains(t, discard, "discarded to `/dev/null`")

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
	deleted := claudeOutputMessage(outputSink{kind: sinkPipe, detail: "cat"},
		[]string{"/repo/build/mytool", "/repo/build/mytool_linux_amd64"})
	assert.Contains(t, deleted, "DELETED")
	assert.Contains(t, deleted, "/repo/build/mytool")
	assert.Contains(t, deleted, "/repo/build/mytool_linux_amd64")
}

func TestClaudeOutputViolation(t *testing.T) {
	origUnder, origSink := runningUnderClaudeFn, inspectStdoutFn
	t.Cleanup(func() { runningUnderClaudeFn, inspectStdoutFn = origUnder, origSink })

	// Not running under Claude: never a violation, whatever the sink.
	runningUnderClaudeFn = func() bool { return false }
	inspectStdoutFn = func() outputSink { return outputSink{kind: sinkPipe, detail: "head"} }
	_, bad := claudeOutputViolation()
	assert.False(t, bad, "no violation when not running under Claude")

	// Under Claude with visible output (a terminal or the harness capture):
	// allowed. The exiting wrapper must be a no-op on this path — it would
	// os.Exit the test process otherwise.
	runningUnderClaudeFn = func() bool { return true }
	inspectStdoutFn = func() outputSink { return outputSink{kind: sinkVisible} }
	_, bad = claudeOutputViolation()
	assert.False(t, bad, "visible output is not a violation")
	guardAgainstClaudeOutputCapture()

	// Under Claude with hidden output: a violation, reported with its sink.
	// There is no env var or flag that can turn this off.
	inspectStdoutFn = func() outputSink { return outputSink{kind: sinkPipe, detail: "head"} }
	s, bad := claudeOutputViolation()
	assert.True(t, bad, "captured output under Claude is a violation")
	assert.Equal(t, sinkPipe, s.kind)
	assert.Equal(t, "head", s.detail)
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
