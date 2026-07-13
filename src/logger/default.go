package logger

import (
	"io"
	"os"
	"sync"
)

var (
	defaultMu     sync.RWMutex
	defaultLogger *Logger
)

// stdoutWriter is an io.Writer that always delegates to the current os.Stdout.
// This allows tests to replace os.Stdout after logger initialization.
type stdoutWriter struct{}

func (stdoutWriter) Write(p []byte) (int, error) { return os.Stdout.Write(p) }

// stderrWriter is an io.Writer that always delegates to the current os.Stderr.
type stderrWriter struct{}

func (stderrWriter) Write(p []byte) (int, error) { return os.Stderr.Write(p) }

// Default returns the global default logger. If Init has not been called,
// it returns a safe pre-init logger that writes to os.Stderr (for Debug/Warn/Error)
// and os.Stdout (for Info/Output) at LevelInfo.
func Default() *Logger {
	defaultMu.RLock()
	l := defaultLogger
	defaultMu.RUnlock()
	if l != nil {
		return l
	}
	// Lazily construct a safe pre-init default.
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if defaultLogger == nil {
		// Use indirect writers so that tests replacing os.Stdout/os.Stderr
		// are reflected in subsequent logger writes without re-initializing.
		// GHAAuto=true means GHA mode is checked dynamically at emit time,
		// so tests that set GITHUB_ACTIONS after init are correctly handled.
		defaultLogger = New(Options{
			Level:   LevelInfo,
			Stdout:  stdoutWriter{},
			Stderr:  stderrWriter{},
			GHAAuto: true,
		})
	}
	return defaultLogger
}

// Init replaces the global default logger with a new one built from opts.
// Returns the new logger for convenience. Safe to call from multiple goroutines.
func Init(opts Options) *Logger {
	if opts.Stdout == nil {
		opts.Stdout = stdoutWriter{}
	}
	if opts.Stderr == nil {
		opts.Stderr = stderrWriter{}
	}
	l := &Logger{opts: opts}
	defaultMu.Lock()
	defaultLogger = l
	defaultMu.Unlock()
	return l
}

// Ensure the indirect writers implement io.Writer.
var _ io.Writer = stdoutWriter{}
var _ io.Writer = stderrWriter{}

// Package-level convenience functions that forward to Default().

// Debug emits a Debug message on the default logger.
func Debug(format string, args ...any) { Default().Debug(format, args...) }

// Info emits an Info message on the default logger.
func Info(format string, args ...any) { Default().Info(format, args...) }

// Warn emits a Warn message on the default logger.
func Warn(format string, args ...any) { Default().Warn(format, args...) }

// WarnFile emits a file-annotated warning on the default logger.
func WarnFile(file, format string, args ...any) { Default().WarnFile(file, format, args...) }

// Error emits an Error message on the default logger.
func Error(format string, args ...any) { Default().Error(format, args...) }

// ErrorFile emits a file-annotated error on the default logger.
func ErrorFile(file, format string, args ...any) { Default().ErrorFile(file, format, args...) }

// Output emits an unconditional Output message on the default logger.
func Output(format string, args ...any) { Default().Output(format, args...) }

// WithSubsystem returns a child of the default logger with a subsystem prefix.
func WithSubsystem(name string) *Logger { return Default().WithSubsystem(name) }
