package runner

import (
	"io"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The mechanism, proven against exec's own pipe. A grandchild inherits the
// child's stdout, so the write end outlives the child and EOF never arrives.
func TestExecStdoutPipeWedgesOnALingeringGrandchild(t *testing.T) {
	cmd := exec.Command("sh", "-c", "echo hello; sleep 60 & exit 0")
	pipe, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	defer cmd.Process.Kill()

	done := make(chan struct{})
	go func() {
		io.ReadAll(pipe)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("expected the read to wedge; the grandchild should hold the write end")
	case <-time.After(3 * time.Second):
	}
}

// The runner bounds that wedge, so the same shape returns the output it got.
func TestStdoutReadEndsWhenAGrandchildHoldsThePipe(t *testing.T) {
	restore := drainGrace
	drainGrace = 300 * time.Millisecond
	defer func() { drainGrace = restore }()

	// sh prints, then leaves a background sleep holding the same stdout.
	proc, err := Cmd("sh", "-c", "echo hello; sleep 60 & exit 0").Run(New())
	require.NoError(t, err)

	done := make(chan []byte, 1)
	go func() {
		out, _ := io.ReadAll(proc.Stdout())
		done <- out
	}()

	select {
	case out := <-done:
		require.Contains(t, string(out), "hello")
	case <-time.After(30 * time.Second):
		t.Fatal("read never returned: the grandchild's write end wedged it")
	}
	require.NoError(t, proc.Wait())
}

// The ordinary case must still reach a real EOF, not wait out drainGrace.
func TestStdoutReachesEOFWithoutWaitingOutTheGrace(t *testing.T) {
	restore := drainGrace
	drainGrace = 30 * time.Second
	defer func() { drainGrace = restore }()

	start := time.Now()
	proc, err := Cmd("sh", "-c", "echo quick").Run(New())
	require.NoError(t, err)
	out, err := io.ReadAll(proc.Stdout())
	require.NoError(t, err)
	require.Contains(t, string(out), "quick")
	require.NoError(t, proc.Wait())
	require.Less(t, time.Since(start), 10*time.Second)
}

// A failing command still reports its exit error through the bounded path.
func TestExitErrorSurvivesTheBoundedDrain(t *testing.T) {
	proc, err := Cmd("sh", "-c", "echo out; exit 3").Run(New())
	require.NoError(t, err)
	out, _ := io.ReadAll(proc.Stdout())
	require.Contains(t, string(out), "out")
	require.Error(t, proc.Wait())
}
