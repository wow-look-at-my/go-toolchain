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

// TestWatchdogDisabledByEnv pins the GO_TOOLCHAIN_NO_WATCHDOG=1 off-switch:
// startWatchdog must decline to touch fd 1/2 and return nil (the build then
// runs on its real stdio). Only the exact value "1" disables.
func TestWatchdogDisabledByEnv(t *testing.T) {
	t.Setenv("GO_TOOLCHAIN_NO_WATCHDOG", "1")
	require.True(t, watchdogDisabled())
	require.Nil(t, startWatchdog(time.Second))

	t.Setenv("GO_TOOLCHAIN_NO_WATCHDOG", "")
	require.False(t, watchdogDisabled())
	t.Setenv("GO_TOOLCHAIN_NO_WATCHDOG", "0")
	require.False(t, watchdogDisabled())
}

// TestWatchdogStopDoesNotDropBufferedOutput is a regression test for the pipe
// drain race at shutdown: output written just before wd.stop() must still make
// it through to the original stdout. Without the forward-goroutine wait in
// stop(), stdoutR.Close() discarded any bytes forward() hadn't read yet,
// causing the coverage block to vanish intermittently.
func TestWatchdogStopDoesNotDropBufferedOutput(t *testing.T) {
	// Forces single-threaded scheduling so forward() and main compete for one P; otherwise the race rarely triggers.
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

		// Redirects fd 1/2 to the capture pipes before startWatchdog, so its saved origStdout/origStderr become our targets.
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
