//go:build unix

package cmd

import (
	"context"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// startWatchdog replaces fd 1 (stdout) and fd 2 (stderr) with pipes,
// forwarding all output to the original file descriptors while monitoring
// for stalls. Returns nil if setup fails (non-fatal; build continues without monitoring).
func startWatchdog(threshold time.Duration) *outputWatchdog {
	// Save original file descriptors
	origStdoutFd, err := unix.Dup(1)
	if err != nil {
		return nil
	}
	origStderrFd, err := unix.Dup(2)
	if err != nil {
		unix.Close(origStdoutFd)
		return nil
	}

	// Create pipes for stdout and stderr
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		unix.Close(origStdoutFd)
		unix.Close(origStderrFd)
		return nil
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		stdoutR.Close()
		stdoutW.Close()
		unix.Close(origStdoutFd)
		unix.Close(origStderrFd)
		return nil
	}

	// Replace fd 1 and 2 with pipe write-ends
	if err := unix.Dup2(int(stdoutW.Fd()), 1); err != nil {
		stdoutR.Close()
		stdoutW.Close()
		stderrR.Close()
		stderrW.Close()
		unix.Close(origStdoutFd)
		unix.Close(origStderrFd)
		return nil
	}
	if err := unix.Dup2(int(stderrW.Fd()), 2); err != nil {
		// Restore stdout before bailing
		unix.Dup2(origStdoutFd, 1)
		stdoutR.Close()
		stdoutW.Close()
		stderrR.Close()
		stderrW.Close()
		unix.Close(origStdoutFd)
		unix.Close(origStderrFd)
		return nil
	}

	// Update Go's global file handles to use the new fds
	os.Stdout = os.NewFile(1, "/dev/stdout")
	os.Stderr = os.NewFile(2, "/dev/stderr")

	// Close the extra write-end file handles; fd 1 and 2 are copies now
	stdoutW.Close()
	stderrW.Close()

	w := &outputWatchdog{
		origStdout: os.NewFile(uintptr(origStdoutFd), "origStdout"),
		origStderr: os.NewFile(uintptr(origStderrFd), "origStderr"),
		stdoutR:    stdoutR,
		stderrR:    stderrR,
		threshold:  threshold,
		done:       make(chan struct{}),
	}
	w.lastOutput.Store(time.Now().UnixNano())

	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel

	go w.forward(stdoutR, w.origStdout)
	go w.forward(stderrR, w.origStderr)
	go w.watchLoop(ctx)

	return w
}

// stop restores original file descriptors and shuts down the watchdog.
func (w *outputWatchdog) stop() {
	if w == nil {
		return
	}

	// Flush any pending writes
	os.Stdout.Sync()
	os.Stderr.Sync()

	// Restore original file descriptors
	unix.Dup2(int(w.origStdout.Fd()), 1)
	unix.Dup2(int(w.origStderr.Fd()), 2)
	os.Stdout = os.NewFile(1, "/dev/stdout")
	os.Stderr = os.NewFile(2, "/dev/stderr")

	// Close pipe read-ends to unblock forward goroutines
	w.stdoutR.Close()
	w.stderrR.Close()

	// Stop the watch loop
	w.cancel()
	<-w.done

	w.origStdout.Close()
	w.origStderr.Close()
}
