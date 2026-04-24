// Package logx installs a process-wide elapsed-duration pipeline for the
// toolchain's stdout and stderr.
//
// # Why
//
// Toolchain diagnostics are spread across many packages, each using
// fmt.Printf / fmt.Fprintf(os.Stderr, …) directly. Migrating every call
// site to a centralized logger would be invasive and churn-heavy, and it
// would miss the output of child subprocesses (which inherit our
// stdout/stderr FDs). Instead, Install() swaps os.Stdout and os.Stderr for
// pipe write-ends and starts goroutines that read each complete line and
// forward it to the real stream with an elapsed-time suffix (e.g. " 0.19s").
//
// With Install() active, every line emitted by this process — from our own
// fmt.Printf calls, from subprocess output inherited via the pipe, from
// anywhere — arrives on the real terminal with a duration suffix. Call
// sites don't change.
//
// # When not to install
//
// GOCACHEPROG mode must produce raw JSON on stdout for the Go toolchain
// to parse, so Install() must NOT be called in that path. main.go already
// handles that by early-returning from init when cacheprog is detected.
//
// # Ordering
//
// Install() must run before any code writes to stdout/stderr. It's
// idempotent — subsequent calls are no-ops.
//
// Flush() should be deferred from main() so partial lines and buffered
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

const (
	colorReset    = "\033[0m"
	colorDimCyan  = "\033[38;2;100;160;160m"
)

// Stdout / Stderr are dynamic forwarders to the current os.Stdout /
// os.Stderr. Using these as io.Writer destinations is equivalent to
// writing to os.Stdout / os.Stderr directly, but signals "log-like"
// intent at the call site. Post-Install() both paths flow through the
// timestamp pipe.
type stdoutForwarder struct{}

func (stdoutForwarder) Write(p []byte) (int, error) { return os.Stdout.Write(p) }

type stderrForwarder struct{}

func (stderrForwarder) Write(p []byte) (int, error) { return os.Stderr.Write(p) }

var (
	Stdout io.Writer = stdoutForwarder{}
	Stderr io.Writer = stderrForwarder{}
)

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
// duration suffix (e.g. output from cmd/console.go's fmtDuration, used
// by step.finish). We skip adding another duration in that case.
var alreadyTimedRE = regexp.MustCompile(` \d+\.\d{2}s$`)

// drain reads lines from r and writes them to w with an elapsed-duration
// suffix. The duration is the wall-clock gap between successive lines —
// the same semantics as timedLineWriter in cmd/console.go.
//
// Lines that already end with a " X.XXs" duration (after stripping ANSI
// color codes) are passed through unchanged, so we don't double-stamp
// output from step.finish or other places that already format timing.
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
			if alreadyTimedRE.MatchString(stripped) {
				fmt.Fprintln(w, content)
			} else {
				elapsed := time.Since(lineStart)
				fmt.Fprintf(w, "%s %s\n", content, fmtElapsed(elapsed))
			}
			lineStart = time.Now()
		}
		if err != nil {
			return
		}
	}
}

func fmtElapsed(d time.Duration) string {
	return fmt.Sprintf("%s%.2fs%s", colorDimCyan, d.Seconds(), colorReset)
}

// Logf writes a formatted message to os.Stderr with a trailing newline
// (appended if missing). When Install() has been called, the drainer
// appends an elapsed-duration suffix.
func Logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	io.WriteString(os.Stderr, msg)
}

// Logln is fmt.Fprintln(os.Stderr, args...).
func Logln(args ...any) {
	fmt.Fprintln(os.Stderr, args...)
}

// Printf is Logf but writes to os.Stdout.
func Printf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	io.WriteString(os.Stdout, msg)
}
