// Package logger provides a centralized, leveled logging facility for
// go-toolchain. It routes messages to the appropriate stream (stdout/stderr),
// honors GitHub Actions workflow commands, and supports optional ANSI colors
// and subsystem prefixes.
//
// All writes to os.Stdout and os.Stderr in the codebase must go through this
// package — direct fmt.Printf / fmt.Fprintf(os.Stdout|Stderr, ...) calls are
// banned by the bannedoutput vet analyzer.
package logger

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// Level represents the minimum severity a Logger will emit.
type Level int8

const (
	// LevelDebug enables all messages.
	LevelDebug Level = iota - 1
	// LevelInfo is the default: Debug messages are suppressed.
	LevelInfo
	// LevelWarn suppresses Info and Debug messages.
	LevelWarn
	// LevelError suppresses Info, Debug and Warn messages.
	LevelError
	// LevelSilent suppresses everything except Output (unconditional).
	LevelSilent
)

// ParseLevel converts a string to a Level. Returns an error if unknown.
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return LevelDebug, nil
	case "info":
		return LevelInfo, nil
	case "warn", "warning":
		return LevelWarn, nil
	case "error":
		return LevelError, nil
	case "silent":
		return LevelSilent, nil
	default:
		return LevelInfo, fmt.Errorf("unknown log level %q (valid: debug, info, warn, error, silent)", s)
	}
}

// Options configures a Logger.
type Options struct {
	// Level is the minimum severity to emit. Defaults to LevelInfo.
	Level Level
	// Stdout is the writer for Output and Info messages. Defaults to os.Stdout.
	Stdout io.Writer
	// Stderr is the writer for Debug, Warn, and Error messages. Defaults to os.Stderr.
	Stderr io.Writer
	// GHA enables GitHub Actions workflow commands (::warning, ::error).
	// Defaults to true when GITHUB_ACTIONS=="true".
	GHA bool
	// GHAAuto, when true, overrides GHA and checks GITHUB_ACTIONS at emit
	// time instead of at init time. Used by the default logger so that tests
	// which set GITHUB_ACTIONS after logger initialization are respected.
	GHAAuto bool
	// Colors enables ANSI color codes in output. Defaults to false in non-TTY
	// environments. Set explicitly to override auto-detection.
	Colors bool
}

// Logger is a leveled logger. It is safe to use from multiple goroutines.
type Logger struct {
	opts      Options
	subsystem string
	mu        sync.Mutex
}

// New creates a Logger with the given options.
func New(opts Options) *Logger {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	return &Logger{opts: opts}
}

// WithSubsystem returns a child logger that prefixes every message with
// "<name>: ". The child shares the same underlying writers and level.
func (l *Logger) WithSubsystem(name string) *Logger {
	l.mu.Lock()
	opts := l.opts
	l.mu.Unlock()
	return &Logger{opts: opts, subsystem: name}
}

// Debug emits a message to Stderr when level <= LevelDebug.
func (l *Logger) Debug(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.opts.Level > LevelDebug {
		return
	}
	l.write(l.opts.Stderr, format, args...)
}

// Info emits a message to Stdout when level <= LevelInfo.
func (l *Logger) Info(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.opts.Level > LevelInfo {
		return
	}
	l.write(l.opts.Stdout, format, args...)
}

// isGHA returns true if GitHub Actions mode is active. When GHAAuto is set
// it checks GITHUB_ACTIONS at call time (used by the default logger so that
// tests setting the env var after init are respected); otherwise it returns
// the static GHA option.
// Callers must hold l.mu.
func (l *Logger) isGHA() bool {
	if l.opts.GHAAuto {
		return os.Getenv("GITHUB_ACTIONS") == "true"
	}
	return l.opts.GHA
}

// Warn emits a message to Stderr (or a GHA ::warning annotation) when
// level <= LevelWarn. Every emitted warning increments the process-wide
// warning counter (see WarnCount) and therefore counts against the build's
// warnings budget — use it only for conditions the project being built can
// actually fix in its own tree. For infrastructure conditions (see
// WarnInfra), use WarnInfra instead.
func (l *Logger) Warn(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warn("", true, format, args...)
}

// WarnFile emits a file-annotated warning (GHA: file=<file>::<msg>,
// locally: same as Warn). level <= LevelWarn required. Every emitted warning
// increments the process-wide warning counter (see WarnCount).
func (l *Logger) WarnFile(file, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warn(file, true, format, args...)
}

// WarnInfra emits a warning that is printed exactly like Warn but does NOT
// count against the build's warnings budget (see WarnCount).
//
// It is for INFRASTRUCTURE conditions: the shared build cache degrading, a
// remote endpoint being slow or unreachable, a cache object being refused and
// re-fetched, a FUSE mount being unavailable. Those messages are diagnostics
// about the machine and the network, not about the source tree being built:
//
//   - They are not actionable by the project — no edit to its code makes them
//     go away.
//   - They are nondeterministic. Counting them makes the warnings gate flaky:
//     the same commit passes or fails depending on network weather, which is
//     exactly the kind of dishonest signal the gate exists to prevent.
//
// The message is still printed in full (and still annotates in GitHub
// Actions) — it is demoted from the budget, not hidden.
func (l *Logger) WarnInfra(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warn("", false, format, args...)
}

// warn is the shared emit path for Warn, WarnFile, and WarnInfra. budgeted
// selects whether the message counts against the warnings budget.
// Callers must hold l.mu.
func (l *Logger) warn(file string, budgeted bool, format string, args ...any) {
	if l.opts.Level > LevelWarn {
		return
	}
	if budgeted {
		warnCount.Add(1)
	}
	msg := l.format(format, args...)
	if l.isGHA() {
		EmitGHAWarning(l.opts.Stdout, file, msg)
		return
	}
	if l.opts.Colors {
		fmt.Fprintf(l.opts.Stderr, "  \033[38;2;255;255;0mWARNING: %s\033[0m\n", msg)
	} else {
		fmt.Fprintf(l.opts.Stderr, "  WARNING: %s\n", msg)
	}
}

// Error emits a message to Stderr (or a GHA ::error annotation) when
// level <= LevelError.
func (l *Logger) Error(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.opts.Level > LevelError {
		return
	}
	msg := l.format(format, args...)
	if l.isGHA() {
		EmitGHAError(l.opts.Stdout, "", msg)
		return
	}
	if l.opts.Colors {
		fmt.Fprintf(l.opts.Stderr, "  \033[38;2;255;0;0mERROR: %s\033[0m\n", msg)
	} else {
		fmt.Fprintf(l.opts.Stderr, "  ERROR: %s\n", msg)
	}
}

// ErrorFile emits a file-annotated error (GHA: file=<file>::<msg>).
// level <= LevelError required.
func (l *Logger) ErrorFile(file, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.opts.Level > LevelError {
		return
	}
	msg := l.format(format, args...)
	if l.isGHA() {
		EmitGHAError(l.opts.Stdout, file, msg)
		return
	}
	if l.opts.Colors {
		fmt.Fprintf(l.opts.Stderr, "  \033[38;2;255;0;0mERROR: %s\033[0m\n", msg)
	} else {
		fmt.Fprintf(l.opts.Stderr, "  ERROR: %s\n", msg)
	}
}

// Output emits a message unconditionally to Stdout. This is for "the actual
// result of the command" (e.g. version strings, coverage percentages). It
// bypasses level filtering — even LevelSilent does not suppress it.
func (l *Logger) Output(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.write(l.opts.Stdout, format, args...)
}

// format builds the final message string, prepending the subsystem prefix if set.
// Callers must hold l.mu.
func (l *Logger) format(format string, args ...any) string {
	msg := fmt.Sprintf(format, args...)
	if l.subsystem != "" {
		msg = l.subsystem + ": " + msg
	}
	return msg
}

// write formats and writes a message with a trailing newline.
// Callers must hold l.mu.
func (l *Logger) write(w io.Writer, format string, args ...any) {
	msg := l.format(format, args...)
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	fmt.Fprint(w, msg)
}
