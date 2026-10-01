//go:build unix

package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// TestWatchdogStartsOnEveryRun pins that nothing in the environment can
// decline stall monitoring: a build that cannot be told it went silent is
// exactly the build that goes silent for minutes and reports nothing.
func TestWatchdogStartsOnEveryRun(t *testing.T) {
	t.Serial()
	wd := startWatchdog(time.Second)
	require.NotNil(t, wd)
	wd.stop()
}

// TestWatchdogWarnsWhileTheBuildIsSilent covers the watchdog's whole reason to
// exist, which nothing asserted: a phase that prints nothing past the threshold
// has to produce the STALLED banner on the real stderr. A test phase went quiet
// for 200s against a 5s threshold and no banner appeared, and there was no test
// that would have caught it.
func TestWatchdogWarnsWhileTheBuildIsSilent(t *testing.T) {
	t.Serial()

	savedStdoutFd, err := unix.Dup(1)
	require.NoError(t, err, "dup saved stdout")
	savedStderrFd, err := unix.Dup(2)
	require.NoError(t, err, "dup saved stderr")
	savedStdout, savedStderr := os.Stdout, os.Stderr
	t.Cleanup(func() {
		unix.Dup2(savedStdoutFd, 1)
		unix.Dup2(savedStderrFd, 2)
		unix.Close(savedStdoutFd)
		unix.Close(savedStderrFd)
		os.Stdout = savedStdout
		os.Stderr = savedStderr
	})

	outR, outW, err := os.Pipe()
	require.NoError(t, err, "pipe out")
	errR, errW, err := os.Pipe()
	require.NoError(t, err, "pipe err")

	// The watchdog saves whatever fd 1 and 2 hold when it starts, so these
	// capture pipes become its origStdout and origStderr: the banner lands in
	// errR even though the build's own stderr is the watchdog's pipe.
	require.NoError(t, unix.Dup2(int(outW.Fd()), 1), "dup2 out")
	require.NoError(t, unix.Dup2(int(errW.Fd()), 2), "dup2 err")
	outW.Close()
	errW.Close()

	watchdog := startWatchdog(200 * time.Millisecond)
	require.NotNil(t, watchdog, "startWatchdog returned nil")
	watchdog.setStep("Running tests with coverage")

	// Print nothing at all: this is the silent compile the banner is for.
	time.Sleep(2 * time.Second)
	watchdog.stop()

	unix.Dup2(savedStdoutFd, 1)
	unix.Dup2(savedStderrFd, 2)
	os.Stdout = savedStdout
	os.Stderr = savedStderr

	var gotOut, gotErr bytes.Buffer
	io.Copy(&gotOut, outR)
	outR.Close()
	io.Copy(&gotErr, errR)
	errR.Close()

	require.Contains(t, gotErr.String(), "STALLED", "no stall banner after 2s of silence at a 200ms threshold")
	require.Contains(t, gotErr.String(), "Running tests with coverage", "the banner did not name the running step")
}

// TestWatchdogStopDoesNotDropBufferedOutput is a regression test for the pipe
// drain race at shutdown: output written just before wd.stop() must still make
// it through to the original stdout. Without the forward-goroutine wait in
// stop(), stdoutR.Close() discarded any bytes forward() hadn't read yet,
// causing the coverage block to vanish intermittently.
func TestWatchdogStopDoesNotDropBufferedOutput(t *testing.T) {
	t.Serial()
	// Forces single-threaded scheduling so forward() and main compete for the same P; otherwise the race rarely triggers.
	prevProcs := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(prevProcs) })

	savedStdoutFd, err := unix.Dup(1)
	require.NoError(t, err, "dup saved stdout")
	savedStderrFd, err := unix.Dup(2)
	require.NoError(t, err, "dup saved stderr")

	savedStdout, savedStderr := os.Stdout, os.Stderr
	t.Cleanup(func() {
		unix.Dup2(savedStdoutFd, 1)
		unix.Dup2(savedStderrFd, 2)
		unix.Close(savedStdoutFd)
		unix.Close(savedStderrFd)
		os.Stdout = savedStdout
		os.Stderr = savedStderr
	})

	const iterations = 200
	const sentinel = "SENTINEL_COVERAGE_BLOCK"

	for i := 0; i < iterations; i++ {
		outR, outW, err := os.Pipe()
		require.NoError(t, err, "iter %d: pipe out", i)
		errR, errW, err := os.Pipe()
		require.NoError(t, err, "iter %d: pipe err", i)

		// Redirects stdout/stderr to the capture pipes before startWatchdog, so its saved origStdout/origStderr become our targets.
		require.NoError(t, unix.Dup2(int(outW.Fd()), 1), "iter %d: dup2 out", i)
		require.NoError(t, unix.Dup2(int(errW.Fd()), 2), "iter %d: dup2 err", i)
		outW.Close()
		errW.Close()

		wd := startWatchdog(5 * time.Second)
		require.NotNil(t, wd, "iter %d: startWatchdog returned nil", i)

		fmt.Fprintln(os.Stdout, sentinel)
		wd.stop()

		// Restore real stdout/stderr so the pipe readers see EOF.
		unix.Dup2(savedStdoutFd, 1)
		unix.Dup2(savedStderrFd, 2)
		os.Stdout = savedStdout
		os.Stderr = savedStderr

		var got bytes.Buffer
		io.Copy(&got, outR)
		outR.Close()
		var gotErr bytes.Buffer
		io.Copy(&gotErr, errR)
		errR.Close()

		require.Contains(t, got.String(), sentinel, "iter %d/%d: sentinel missing from forwarded stdout", i+1, iterations)
	}
}
