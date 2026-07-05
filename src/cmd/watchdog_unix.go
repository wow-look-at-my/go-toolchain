//go:build unix && !cosmo

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
	if watchdogDisabled() {
		return nil
	}
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

	// The existing os.Stdout / os.Stderr *os.File values already have
	// Fd() == 1 / 2, so the Dup2 above is enough — writes through them
	// now reach the pipe. Do NOT reassign via os.NewFile(1, …): that
	// attaches a close-on-GC finalizer, and repeated watchdog cycles
	// (e.g. TestWatchdogStop*'s 200-iteration loop) leave behind enough
	// wrappers that their finalizers eventually close the real
	// stdout/stderr out from under later runtime code — notably the
	// -coverpkg atexit profile writer, which then silently fails the
	// whole package.

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

	// Restore original file descriptors. Dup2 drops the last writer refcount on
	// each pipe, so forward() will drain the kernel buffer and return on EOF.
	unix.Dup2(int(w.origStdout.Fd()), 1)
	unix.Dup2(int(w.origStderr.Fd()), 2)
	// No os.NewFile reassignment needed: os.Stdout/os.Stderr already
	// reference fd 1/2, which now point back to the original stdio.
	// Avoid it for the same finalizer-accumulation reason noted in
	// startWatchdog.

	// Must wait for forward() before closing read-ends; otherwise buffered
	// output in the pipe (e.g. the final coverage block) is discarded.
	w.fwdWG.Wait()
	w.stdoutR.Close()
	w.stderrR.Close()

	// Stop the watch loop
	w.cancel()
	<-w.done

	w.origStdout.Close()
	w.origStderr.Close()
}
