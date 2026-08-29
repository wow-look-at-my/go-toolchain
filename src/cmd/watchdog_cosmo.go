//go:build cosmo

package cmd

import (
	"context"
	"os"
	"syscall"
	"time"
)

// GOOS=cosmo (gosmopolitan) counts as `unix`, but golang.org/x/sys/unix has
// no cosmo port, so this file mirrors watchdog_unix.go using the fork's
// stdlib syscall package instead: the cosmo port exposes Dup, Dup2 (via
// Dup3), and Close directly. Keep both implementations in sync.

// startWatchdog replaces the stdout and stderr descriptors with pipes,
// forwarding all output to the original file descriptors while monitoring
// for stalls. Returns nil if setup fails (non-fatal; build continues without monitoring).
func startWatchdog(threshold time.Duration) *outputWatchdog {
	if watchdogDisabled() {
		return nil
	}
	// Save original file descriptors
	origStdoutFd, err := syscall.Dup(1)
	if err != nil {
		return nil
	}
	origStderrFd, err := syscall.Dup(2)
	if err != nil {
		syscall.Close(origStdoutFd)
		return nil
	}

	// Create pipes for stdout and stderr
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		syscall.Close(origStdoutFd)
		syscall.Close(origStderrFd)
		return nil
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		stdoutR.Close()
		stdoutW.Close()
		syscall.Close(origStdoutFd)
		syscall.Close(origStderrFd)
		return nil
	}

	// Replace the stdout and stderr descriptors with pipe write-ends
	if err := syscall.Dup2(int(stdoutW.Fd()), 1); err != nil {
		stdoutR.Close()
		stdoutW.Close()
		stderrR.Close()
		stderrW.Close()
		syscall.Close(origStdoutFd)
		syscall.Close(origStderrFd)
		return nil
	}
	if err := syscall.Dup2(int(stderrW.Fd()), 2); err != nil {
		// Restore stdout before bailing
		syscall.Dup2(origStdoutFd, 1)
		stdoutR.Close()
		stdoutW.Close()
		stderrR.Close()
		stderrW.Close()
		syscall.Close(origStdoutFd)
		syscall.Close(origStderrFd)
		return nil
	}

	// Never reassign os.Stdout/Stderr via os.NewFile: piled-up finalizers eventually close real stdio out from under later code.

	// Close the extra write-end file handles; the stdio descriptors are copies now
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

	w.fwdWG.Add(2)
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

	// Dup2 drops the last writer refcount on each pipe, so forward() drains the kernel buffer and returns on EOF.
	syscall.Dup2(int(w.origStdout.Fd()), 1)
	syscall.Dup2(int(w.origStderr.Fd()), 2)
	// No os.NewFile reassignment needed here either -- avoid it for the same finalizer-accumulation reason as startWatchdog.

	// Wait for forward() before closing read-ends, or buffered pipe output (e.g. the final coverage block) is discarded.
	w.fwdWG.Wait()
	w.stdoutR.Close()
	w.stderrR.Close()

	// Stop the watch loop
	w.cancel()
	<-w.done

	w.origStdout.Close()
	w.origStderr.Close()
}
