// Package logx installs a process-wide elapsed-duration pipeline for the
// toolchain's stdout and stderr.
//
// # Why
//
// Install() swaps os.Stdout and os.Stderr for pipe write-ends and starts
// goroutines that read each complete line and forward it to the real stream
// with an elapsed-time suffix (e.g. " 0.19s") — but only for lines that took
// at least a second to appear. Faster lines print unchanged, so the suffix
// marks the handful of lines actually worth timing instead of cluttering
// every line with a "0.01s".
//
// With Install() active, every line emitted by this process — from our own
// logger calls, from subprocess output inherited via the pipe, from anywhere
// — arrives on the real terminal, timed if it was slow to appear. Call sites
// don't change.
//
// # When not to install
//
// GOCACHEPROG mode must produce raw JSON on stdout for the Go toolchain
// to parse, so Install() must NOT be called in that path. main.go already
// handles that by skipping Install() when cacheprog is detected.
//
// # Ordering
//
// Install() must run before any code writes to stdout/stderr. It's
// idempotent — subsequent calls are no-ops.
//
// Flush() should be called on every exit path so partial lines and buffered
// pipe content are emitted before exit.
package logx

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ColorDimCyan is the ANSI color used for elapsed-duration suffixes.
// Exported so cmd's tests can assert on the expected formatting.
const ColorDimCyan = "\033[38;2;100;160;160m"

const colorReset = "\033[0m"

var (
	installOnce sync.Once
	installed   bool
	origStdout  *os.File
	origStderr  *os.File
	pipeStdoutW *os.File
	pipeStderrW *os.File
	drainedWG   sync.WaitGroup
)

// Install redirects os.Stdout and os.Stderr through pipes. A goroutine
// per stream reads complete lines and writes them back to the original
// stream with an elapsed-duration suffix (e.g. " 0.19s").
//
// Do NOT call this in GOCACHEPROG mode — the Go toolchain expects raw
// JSON on stdout there.
func Install() {
	installOnce.Do(func() {
		origStdout = os.Stdout
		origStderr = os.Stderr

		prOut, pwOut, err := os.Pipe()
		if err != nil {
			return
		}
		prErr, pwErr, err := os.Pipe()
		if err != nil {
			prOut.Close()
			pwOut.Close()
			return
		}

		os.Stdout = pwOut
		os.Stderr = pwErr
		pipeStdoutW = pwOut
		pipeStderrW = pwErr

		drainedWG.Add(2)
		go drain(prOut, origStdout)
		go drain(prErr, origStderr)

		installed = true
	})
}

// Flush closes the pipe write-ends and waits for drainer goroutines to
// finish. os.Stdout and os.Stderr are restored to the original files so
// any late writes (e.g. from panic handlers) still reach the terminal
// without deadlocking on a closed pipe.
//
// Safe to call multiple times and before Install().
func Flush() {
	if !installed {
		return
	}
	// Restore originals first so any post-Flush writes don't deadlock on a
	// closed pipe. Then close the pipe write-ends to signal EOF to drainers.
	os.Stdout = origStdout
	os.Stderr = origStderr
	_ = pipeStdoutW.Close()
	_ = pipeStderrW.Close()
	drainedWG.Wait()
	installed = false
}

// ansiRE matches ANSI escape sequences so we can strip them before
// checking whether a line already ends with a duration.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// alreadyTimedRE matches a line that already ends with a " X.XXs"-style
// duration suffix (e.g. output from cmd/console.go's fmtDuration). We skip
// adding another duration in that case.
var alreadyTimedRE = regexp.MustCompile(` \d+\.\d{2}s$`)

// minDurationToShow is the minimum elapsed time before drain appends a
// duration suffix to a line. Most lines (progress messages, help text,
// ordinary log output) appear well under a second apart; stamping every one
// of them would bury the few durations that actually matter under noise, so
// faster lines print unchanged. A var, not a const, so tests can lower it
// instead of sleeping for real.
var minDurationToShow = time.Second

// drain reads lines from r and writes them to w with an elapsed-duration
// suffix (the wall-clock gap since the previous line, formatted by
// FmtDuration) — but only when that gap is at least minDurationToShow.
// Faster lines are written unchanged.
//
// Lines that already end with a " X.XXs" duration (after stripping ANSI
// color codes) are always passed through unchanged, regardless of elapsed
// time, so we don't double-stamp output from step.finish or other places
// that already format timing via fmtDuration.
//
// Partial content at EOF (no trailing newline) is emitted with an
// appended newline so nothing is lost.
func drain(r *os.File, w io.Writer) {
	defer drainedWG.Done()
	defer r.Close()
	br := bufio.NewReader(r)
	lineStart := time.Now()
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			content := strings.TrimRight(line, "\n")
			stripped := ansiRE.ReplaceAllString(content, "")
			elapsed := time.Since(lineStart)
			if alreadyTimedRE.MatchString(stripped) || elapsed < minDurationToShow {
				fmt.Fprintln(w, content)
			} else {
				fmt.Fprintf(w, "%s %s\n", content, FmtDuration(elapsed))
			}
			lineStart = time.Now()
		}
		if err != nil {
			return
		}
	}
}

// FmtDuration formats a duration as dark greyish-cyan colored "X.XXs"
// (e.g. "0.19s"). Used by both the drain and by cmd/console.go's step
// system, so output stays consistent and the drain's already-timed-line
// detection works.
func FmtDuration(d time.Duration) string {
	return fmt.Sprintf("%s%.2fs%s", ColorDimCyan, d.Seconds(), colorReset)
}
