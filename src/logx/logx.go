// Package logx installs a process-wide elapsed-duration pipeline for the
// toolchain's stdout and stderr.
//
// # Why
//
// Install() swaps os.Stdout and os.Stderr for pipe write-ends and starts
// goroutines that read each complete line and forward it to the real stream
// with an elapsed-time suffix — but only for a line slow enough to reach
// minDurationToShow. A faster line prints unchanged, so the suffix
// marks the handful of lines actually worth timing instead of stamping
// an instant duration onto every line.
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

// ColorDimCyan is the ANSI color for elapsed-duration suffixes, exported for tests.
const ColorDimCyan = "\033[38;2;100;160;160m"

const colorReset = "\033[0m"

var (
	// installMu guards installed and the pipe fields under it. A second
	// Install must not overwrite pipeStdoutW while the first install's drain
	// goroutine is still reading the pipe it named: nothing closes that write
	// end afterwards, the goroutine never reaches EOF, and every later Flush
	// blocks in drainedWG.Wait().
	installMu   sync.Mutex
	installed   bool
	origStdout  *os.File
	origStderr  *os.File
	pipeStdoutW *os.File
	pipeStderrW *os.File
	drainedWG   sync.WaitGroup
)

// Install redirects os.Stdout and os.Stderr through pipes. A goroutine
// per stream reads complete lines and writes them back to the original
// stream with an elapsed-duration suffix.
//
// Do NOT call this in GOCACHEPROG mode — the Go toolchain expects raw
// JSON on stdout there.
func Install() {
	installMu.Lock()
	defer installMu.Unlock()
	if installed {
		return
	}

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
}

// Flush closes the pipe write-ends and waits for drainer goroutines to
// finish. os.Stdout and os.Stderr are restored, so a late write reaches the
// terminal, never a closed pipe. Safe to call repeatedly, and before Install().
func Flush() {
	installMu.Lock()
	defer installMu.Unlock()
	if !installed {
		return
	}
	// Restore now so late writes don't deadlock on the pipe after it closes.
	os.Stdout = origStdout
	os.Stderr = origStderr
	_ = pipeStdoutW.Close()
	_ = pipeStderrW.Close()
	drainedWG.Wait()
	installed = false
}

// ansiRE matches ANSI escape sequences, stripped before checking for an existing duration suffix.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// alreadyTimedRE matches a line already ending in a " X.XXs" duration suffix, so drain skips it.
var alreadyTimedRE = regexp.MustCompile(` \d+\.\d{2}s$`)

// minDurationToShow is the elapsed time before drain appends a duration suffix; a var so tests can lower it.
var minDurationToShow = time.Second

// drain reads lines from r and writes them to w, appending an elapsed-duration
// suffix when the gap since the previous line reaches minDurationToShow.
// A line already ending in a duration suffix passes through unchanged, so
// step.finish's own timing is never double-stamped. A trailing partial line
// at EOF is still emitted, with a newline appended.
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

// FmtDuration formats d as dark greyish-cyan "X.XXs", used by both drain
// and console.go's step timing, so output stays consistent.
func FmtDuration(d time.Duration) string {
	return fmt.Sprintf("%s%.2fs%s", ColorDimCyan, d.Seconds(), colorReset)
}
