//go:build darwin

package cmd

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mirrors TestInspectFDClassification (claudeguard_test.go, linux-only): same
// sink decisions, reached through fstat + F_GETPATH instead of /proc.
func TestInspectFDClassificationDarwin(t *testing.T) {
	t.Run("pipe_is_blocked", func(t *testing.T) {
		// darwin cannot identify a pipe's reader (no libproc here), so every
		// pipe fails closed -- there is no allowance to test on the other side.
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
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer r.Close()
	defer w.Close()
	assert.False(t, isTerminal(w.Fd()), "a pipe is not a terminal")
}

// fdPath is darwin's F_GETPATH-based stand-in for /proc/self/fd's readlink;
// this pins that it actually recovers the real path rather than garbage or a
// silently-wrong one.
func TestFDPathRecoversRealPath(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "fdpath-*.log")
	require.NoError(t, err)
	defer f.Close()

	got := fdPath(f.Fd())
	// F_GETPATH can return a firmlink-resolved path (e.g. /private/var/...
	// for a /var/... input) on darwin, so compare os.Stat identity rather than
	// string equality.
	wantInfo, err := os.Stat(f.Name())
	require.NoError(t, err)
	gotInfo, err := os.Stat(got)
	require.NoError(t, err, "fdPath returned %q, which does not exist", got)
	assert.True(t, os.SameFile(wantInfo, gotInfo), "fdPath %q must be the same file as %q", got, f.Name())
}

func TestFDPathEmptyOnPipe(t *testing.T) {
	// A pipe has no path; F_GETPATH must fail rather than return garbage, and
	// fdPath must surface that as "", never a made-up path.
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer r.Close()
	defer w.Close()
	assert.Empty(t, fdPath(w.Fd()))
}

// End-to-end: the actual built binary, run as a real subprocess so its stdout
// fd is a genuine OS-level pipe/file/tty rather than a Go-level *os.File,
// refuses when OPENCODE=1 is set and its output is piped -- the scenario
// dats/cli.dats already covers on linux (matrix.marker), reproduced here
// because that suite does not run on darwin.
func TestAgentGuardRefusesPipedRunUnderOpencode(t *testing.T) {
	bin, err := exec.LookPath("go-toolchain")
	if err != nil {
		t.Skip("go-toolchain not on PATH; build it first")
	}
	dir := t.TempDir()
	cmd := exec.Command("sh", "-c", bin+" 2>&1 | cat")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "OPENCODE=1", "GO_TOOLCHAIN_BUILDHOST_URL=http://127.0.0.1:1")
	out, err := cmd.CombinedOutput()
	require.Error(t, err, "must exit non-zero when piped under OPENCODE=1; got output: %s", out)
	assert.Contains(t, string(out), "refused to run")
	assert.Contains(t, string(out), "opencode")
}
