//go:build darwin

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	agent "github.com/wow-look-at-my/is-this-an-agent"
)

// TestMain intercepts a re-exec of this test binary playing the child whose
// stdout is a socket or pipe the parent holds -- the grok-build / opencode
// topology -- so inspectFD and the guard run against a real far-end pid.
func TestMain(m *testing.M) {
	switch os.Getenv("CLAUDEGUARD_TEST_HELPER") {
	case "inspect_fd1":
		s := inspectFD(1)
		os.Stderr.WriteString("HELPER_KIND=" + strconv.Itoa(int(s.kind)) + " HELPER_DETAIL=" + s.detail + "\n")
		os.Exit(0)
	case "guard":
		if agentName, s, bad := agentOutputViolation(); bad {
			fmt.Fprint(os.Stderr, agentOutputMessage(agentName, s, nil))
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// Mirrors TestInspectFDClassification (claudeguard_test.go, linux-only): same
// sink decisions, reached through fstat + F_GETPATH instead of /proc.
func TestInspectFDClassificationDarwin(t *testing.T) {
	t.Serial()
	t.Run("pipe_is_blocked", func(t *testing.T) {
		// Both ends belong to this process, which is not its own ancestor: fail closed.
		r, w, err := os.Pipe()
		require.NoError(t, err)
		defer r.Close()
		defer w.Close()
		s := inspectFD(w.Fd())
		assert.Equal(t, sinkPipe, s.kind)
	})

	t.Run("plain_file_is_blocked", func(t *testing.T) {
		f, err := os.CreateTemp(t.TempDir(), "out-*.log")
		require.NoError(t, err)
		defer f.Close()
		s := inspectFD(f.Fd())
		assert.Equal(t, sinkFile, s.kind)
		assert.Contains(t, s.detail, ".log")
	})

	t.Run("harness_capture_file_is_allowed", func(t *testing.T) {
		t.Setenv("CLAUDE_CODE_SESSION_ID", "SID-unit-test")
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

func TestIsTerminalOnPipeIsFalseDarwin(t *testing.T) {
	t.Serial()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer r.Close()
	defer w.Close()
	assert.False(t, isTerminal(w.Fd()), "a pipe is not a terminal")
}

// fdPath is darwin's F_GETPATH-based stand-in for /proc/self/fd's readlink;
// this pins that it actually recovers the real path rather than garbage or a
// silently-wrong answer.
func TestFDPathRecoversRealPath(t *testing.T) {
	t.Serial()
	f, err := os.CreateTemp(t.TempDir(), "fdpath-*.log")
	require.NoError(t, err)
	defer f.Close()

	got := fdPath(f.Fd())
	// F_GETPATH may firmlink-resolve the path, so compare os.Stat identity, not strings.
	wantInfo, err := os.Stat(f.Name())
	require.NoError(t, err)
	gotInfo, err := os.Stat(got)
	require.NoError(t, err, "fdPath returned %q, which does not exist", got)
	assert.True(t, os.SameFile(wantInfo, gotInfo), "fdPath %q must be the same file as %q", got, f.Name())
}

func TestFDPathEmptyOnPipe(t *testing.T) {
	t.Serial()
	// A pipe has no path; fdPath must surface "", never a made-up path.
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer r.Close()
	defer w.Close()
	assert.Empty(t, fdPath(w.Fd()))
}

// TestSocketPeerPID pins the raw getsockopt mechanism: both ends of a
// socketpair belong to this same test process, so the peer pid it reports
// must be our own.
func TestSocketPeerPID(t *testing.T) {
	t.Serial()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	require.NoError(t, err)
	defer unix.Close(fds[0])
	defer unix.Close(fds[1])

	pid, ok := socketPeerPID(uintptr(fds[0]))
	require.True(t, ok)
	assert.Equal(t, os.Getpid(), pid)
}

func TestSocketPeerPIDOnNonSocketFails(t *testing.T) {
	t.Serial()
	f, err := os.CreateTemp(t.TempDir(), "notasocket-*")
	require.NoError(t, err)
	defer f.Close()
	_, ok := socketPeerPID(f.Fd())
	assert.False(t, ok)
}

// TestPipeReaderAllowanceThroughTheGuardDarwin mirrors the linux
// TestPipeReaderAllowanceThroughTheGuard, against the sysctl-backed CommPPID
// from is-this-an-agent, isolating the assertion to name/pid matching.
func TestPipeReaderAllowanceThroughTheGuardDarwin(t *testing.T) {
	t.Serial()
	parent := os.Getppid()
	assert.True(t, agent.IsPipeReader("opencode", parent))
	assert.False(t, agent.IsPipeReader("head", parent), "a filter is not the harness")
	assert.False(t, agent.IsPipeReader("opencode", os.Getpid()), "self is not an ancestor")
}

// TestInspectFDSocketClassification exercises the socket branch inspectFD
// added: a socket whose peer resolves but names neither a known agent's comm
// nor a registered agent pid is still blocked, with detail populated (this is
// the diagnostic gap that shipped originally -- sinkHidden carried a detail
// field nothing ever printed).
func TestInspectFDSocketClassification(t *testing.T) {
	t.Serial()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	require.NoError(t, err)
	defer unix.Close(fds[1])
	f := os.NewFile(uintptr(fds[0]), "socket")
	defer f.Close()

	s := inspectFD(f.Fd())
	assert.Equal(t, sinkHidden, s.kind)
	assert.NotEmpty(t, s.detail, "detail must name the peer, not be silently empty")
}

// TestAgentGuardAllowsPlainRunWhenSocketReaderIsTheAgentItself reproduces
// opencode's socketpair stdout plumbing: this process holds the socket's
// other end and is the real parent, with OPENCODE_PID naming its own pid.
func lookPathNativeDarwinToolchain(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("go-toolchain")
	if err != nil {
		t.Skip("go-toolchain not on PATH; build it first")
	}
	out, err := exec.Command("file", "-b", bin).Output()
	if err != nil || !strings.Contains(string(out), "Mach-O") {
		t.Skip("installed go-toolchain is not a native darwin binary")
	}
	return bin
}

func TestAgentGuardAllowsPlainRunWhenSocketReaderIsTheAgentItself(t *testing.T) {
	t.Serial()
	bin := lookPathNativeDarwinToolchain(t)

	runWithSocketStdout := func(t *testing.T, recognizedPID bool) (exitErr error, stderr string) {
		fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
		require.NoError(t, err)
		readerEnd := os.NewFile(uintptr(fds[0]), "socket-reader")
		defer readerEnd.Close()
		childStdout := os.NewFile(uintptr(fds[1]), "socket-writer")
		defer childStdout.Close()

		pidVar := "OPENCODE_PID=0"
		if recognizedPID {
			pidVar = "OPENCODE_PID=" + strconv.Itoa(os.Getpid())
		}
		// A bare invocation reaches the guard; only its own refusal is under test.
		cmd := exec.Command(bin)
		cmd.Dir = t.TempDir()
		cmd.Stdout = childStdout
		var errBuf strings.Builder
		cmd.Stderr = &errBuf
		cmd.Env = append(os.Environ(),
			"OPENCODE=1", pidVar,
			"GO_TOOLCHAIN_BUILDHOST_URL=http://127.0.0.1:1",
		)
		err = cmd.Run()
		childStdout.Close()
		return err, errBuf.String()
	}

	t.Run("recognized_pid_is_allowed_through", func(t *testing.T) {
		err, errOut := runWithSocketStdout(t, true)
		assert.NotContains(t, errOut, "refused to run", "the agent reading its own socket must not be refused; stderr: %s", errOut)
		_ = err // may still fail for unrelated reasons (no go.mod); only the guard's own refusal is under test
	})

	t.Run("unrecognized_pid_is_still_refused", func(t *testing.T) {
		err, errOut := runWithSocketStdout(t, false)
		require.Error(t, err)
		assert.Contains(t, errOut, "refused to run")
		assert.Contains(t, errOut, "opencode")
	})
}

// End-to-end: the actual built binary, run as a real subprocess so its stdout
// fd is a genuine OS-level pipe/file/tty rather than a Go-level *os.File,
// refuses when the OPENCODE marker is set and its output is piped -- the scenario
// dats/cli.dats already covers on linux (matrix.marker), reproduced here
// because that suite does not run on darwin.
func TestAgentGuardRefusesPipedRunUnderOpencode(t *testing.T) {
	t.Serial()
	bin := lookPathNativeDarwinToolchain(t)
	dir := t.TempDir()
	cmd := exec.Command("sh", "-c", "set -o pipefail; \"$1\" 2>&1 | cat", "sh", bin)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "OPENCODE=1", "GO_TOOLCHAIN_BUILDHOST_URL=http://127.0.0.1:1")
	out, err := cmd.CombinedOutput()
	require.Error(t, err, "must exit non-zero when piped under OPENCODE=1; got output: %s", out)
	assert.Contains(t, string(out), "refused to run")
	// Ancestry outranks the env marker, so the message may name grok build.
}

// TestAgentGuardAllowsPlainRunWhenSocketReaderIsGrok is the grok-build twin of
// TestAgentGuardAllowsPlainRunWhenSocketReaderIsTheAgentItself: grok-build
// exports the GROK_AGENT marker (no pid var of its own; measured) and captures stdout
// through a socketpair or a pipe. The parent here holds the far end and names
// itself in GROK_AGENT_PID, the OPENCODE_PID-shaped seam. A real `| cat` still
// refuses.
func TestAgentGuardAllowsPlainRunWhenSocketReaderIsGrok(t *testing.T) {
	t.Serial()
	runWithSocketStdout := func(t *testing.T, recognizedPID bool) (exitErr error, stderr string) {
		fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
		require.NoError(t, err)
		readerEnd := os.NewFile(uintptr(fds[0]), "socket-reader")
		defer readerEnd.Close()
		childStdout := os.NewFile(uintptr(fds[1]), "socket-writer")
		defer childStdout.Close()

		pidVar := grokPIDEnv + "=0"
		if recognizedPID {
			pidVar = grokPIDEnv + "=" + strconv.Itoa(os.Getpid())
		}
		cmd := exec.Command(os.Args[0])
		cmd.Dir = t.TempDir()
		cmd.Stdout = childStdout
		var errBuf strings.Builder
		cmd.Stderr = &errBuf
		cmd.Env = append(os.Environ(),
			"CLAUDEGUARD_TEST_HELPER=guard",
			"GROK_AGENT=1", pidVar,
			"GO_TOOLCHAIN_BUILDHOST_URL=http://127.0.0.1:1",
		)
		err = cmd.Run()
		childStdout.Close()
		return err, errBuf.String()
	}

	t.Run("recognized_pid_is_allowed_through", func(t *testing.T) {
		err, errOut := runWithSocketStdout(t, true)
		assert.NotContains(t, errOut, "refused to run", "the agent reading its own socket must not be refused; stderr: %s", errOut)
		_ = err
	})

	t.Run("unrecognized_pid_is_still_refused", func(t *testing.T) {
		err, errOut := runWithSocketStdout(t, false)
		require.Error(t, err)
		assert.Contains(t, errOut, "refused to run")
		assert.Contains(t, errOut, "grok")
	})
}

func TestAgentGuardAllowsPlainRunWhenPipeReaderIsGrok(t *testing.T) {
	t.Serial()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer r.Close()
	defer w.Close()

	cmd := exec.Command(os.Args[0])
	cmd.Dir = t.TempDir()
	cmd.Stdout = w
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	cmd.Env = append(os.Environ(),
		"CLAUDEGUARD_TEST_HELPER=guard",
		"GROK_AGENT=1", grokPIDEnv+"="+strconv.Itoa(os.Getpid()),
		"GO_TOOLCHAIN_BUILDHOST_URL=http://127.0.0.1:1",
	)
	err = cmd.Start()
	require.NoError(t, err)
	w.Close()
	err = cmd.Wait()
	assert.NotContains(t, errBuf.String(), "refused to run", "the agent reading its own pipe must not be refused; stderr: %s", errBuf.String())
	_ = err
}

func TestAgentGuardRefusesPipedRunUnderGrok(t *testing.T) {
	t.Serial()
	cmd := exec.Command("sh", "-c", "set -o pipefail; \"$1\" 2>&1 | cat", "sh", os.Args[0])
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(),
		"CLAUDEGUARD_TEST_HELPER=guard",
		"GROK_AGENT=1",
		"GO_TOOLCHAIN_BUILDHOST_URL=http://127.0.0.1:1",
	)
	out, err := cmd.CombinedOutput()
	require.Error(t, err, "must exit non-zero when piped under GROK_AGENT=1; got output: %s", out)
	assert.Contains(t, string(out), "refused to run")
	assert.Contains(t, string(out), "grok")
}

func TestPipeHandlesMatchBothEnds(t *testing.T) {
	t.Serial()
	p := make([]int, 2)
	require.NoError(t, unix.Pipe(p))
	defer unix.Close(p[0])
	defer unix.Close(p[1])
	wh, wp, ok := pipeHandles(os.Getpid(), p[1])
	require.True(t, ok)
	rh, rp, ok := pipeHandles(os.Getpid(), p[0])
	require.True(t, ok)
	assert.Equal(t, wh, rp)
	assert.Equal(t, wp, rh)
}
