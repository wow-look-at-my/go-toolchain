package cmd

import (
	"os"
	"os/exec"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterCommandsCoversNamedFilters(t *testing.T) {
	// The five the request calls out explicitly must always be treated as filters.
	for _, name := range []string{"head", "tail", "grep", "sed", "awk"} {
		assert.True(t, filterCommands[name], "%q must be treated as a filter", name)
	}
	// Common consumers that are NOT output-hiding filters must be allowed.
	for _, name := range []string{"go", "cat", "tee", "bash", "sh"} {
		assert.False(t, filterCommands[name], "%q must not be treated as a filter", name)
	}
}

func TestClaudePipeFilterMessageMentionsPeerAndGuidance(t *testing.T) {
	msg := claudePipeFilterMessage("grep")
	assert.Contains(t, msg, "grep", "message should name the offending filter")
	assert.Contains(t, msg, "go-toolchain")
	assert.Contains(t, msg, "NO pipe", "message should tell the agent to drop the pipe")
	assert.Contains(t, msg, ".log", "message should suggest redirecting to a file")
}

func TestClaudePipeFilterViolationDisabledByEnv(t *testing.T) {
	t.Setenv(allowPipeFilterEnv, "1")
	_, bad := claudePipeFilterViolation()
	assert.False(t, bad, "guard must be disabled when %s is set", allowPipeFilterEnv)
	// And the exiting wrapper must be a no-op in that case (it would call
	// os.Exit and crash the test process otherwise).
	guardAgainstClaudePipeFilter()
}

func TestStdoutFilterConsumerRejectsNonFilterPeer(t *testing.T) {
	// stdoutFilterConsumer only reports a violation when the pipe peer is a
	// recognized filter. During `go test`, stdout is a pipe to the go tool
	// (comm "go") or a regular file — never head/tail/grep/sed/awk — so this
	// must report no violation regardless of host.
	_, ok := stdoutFilterConsumer()
	assert.False(t, ok, "go test's stdout consumer must not count as a filter")
}

func TestProcCommPPIDSelf(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("procCommPPID needs /proc (linux)")
	}
	comm, ppid, ok := procCommPPID(os.Getpid())
	require.True(t, ok)
	assert.NotEmpty(t, comm)
	assert.Greater(t, ppid, 0)
}

func TestProcCommPPIDMissing(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("procCommPPID needs /proc (linux)")
	}
	// PID 0 has no /proc entry.
	_, _, ok := procCommPPID(0)
	assert.False(t, ok)
}

func TestPipePeerDetectsConsumer(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pipePeer needs /proc (linux)")
	}
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep not on PATH")
	}
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer w.Close()

	// The child inherits the read end as its stdin, so it holds the same pipe
	// inode as our write end w. exec.Start returns after a successful execve,
	// so by here the child's comm is already "sleep".
	cmd := exec.Command("sleep", "30")
	cmd.Stdin = r
	require.NoError(t, cmd.Start())
	r.Close() // only the child keeps the read end now
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	name, ok := pipePeer(w.Fd())
	require.True(t, ok, "expected to identify the pipe consumer")
	assert.Equal(t, "sleep", name)
}

func TestPipePeerIgnoresNonPipe(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pipePeer needs /proc (linux)")
	}
	f, err := os.CreateTemp(t.TempDir(), "notapipe")
	require.NoError(t, err)
	defer f.Close()
	_, ok := pipePeer(f.Fd())
	assert.False(t, ok, "a regular file must not be detected as a pipe consumer")
}
