package logger

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureLogger builds a Logger that writes to string builders instead of
// os.Stdout / os.Stderr, for easy assertions in tests.
func captureLogger(level Level, gha bool) (*Logger, *strings.Builder, *strings.Builder) {
	out := &strings.Builder{}
	err := &strings.Builder{}
	l := New(Options{
		Level:  level,
		Stdout: out,
		Stderr: err,
		GHA:    gha,
		Colors: false,
	})
	return l, out, err
}

// TestLevelFiltering verifies that messages below the configured level are
// suppressed, and messages at or above the level are emitted.
func TestLevelFiltering(t *testing.T) {
	l, out, errBuf := captureLogger(LevelInfo, false)

	l.Debug("should be hidden")
	assert.Equal(t, 0, errBuf.Len())

	l.Info("should appear on stdout")
	assert.Contains(t, out.String(), "should appear on stdout")

	l.Warn("should appear on stderr")
	assert.Contains(t, errBuf.String(), "should appear on stderr")

	errBuf.Reset()
	l.Error("also stderr")
	assert.Contains(t, errBuf.String(), "also stderr")

}

// TestDebugLevel verifies all messages appear when level is Debug.
func TestDebugLevel(t *testing.T) {
	l, out, errBuf := captureLogger(LevelDebug, false)

	l.Debug("debug msg")
	assert.Contains(t, errBuf.String(), "debug msg")

	l.Info("info msg")
	assert.Contains(t, out.String(), "info msg")

}

// TestWarnLevel verifies that at LevelWarn, Debug and Info are suppressed.
func TestWarnLevel(t *testing.T) {
	l, out, errBuf := captureLogger(LevelWarn, false)

	l.Debug("hidden debug")
	l.Info("hidden info")
	assert.Equal(t, 0, errBuf.Len())

	assert.Equal(t, 0, out.Len())

	l.Warn("visible warning")
	assert.Contains(t, errBuf.String(), "visible warning")

}

// TestSilentLevel verifies that at LevelSilent only Output is visible.
func TestSilentLevel(t *testing.T) {
	l, out, errBuf := captureLogger(LevelSilent, false)

	l.Debug("no")
	l.Info("no")
	l.Warn("no")
	l.Error("no")

	assert.Equal(t, 0, errBuf.Len())

	assert.Equal(t, 0, out.Len())

	// Output always prints.
	l.Output("real result")
	assert.Contains(t, out.String(), "real result")

}

// TestOutputAlwaysPrints verifies Output ignores level filtering entirely.
func TestOutputAlwaysPrints(t *testing.T) {
	l, out, _ := captureLogger(LevelSilent, false)
	l.Output("unconditional")
	assert.Contains(t, out.String(), "unconditional")

}

// TestSubsystemPrefix verifies that WithSubsystem prepends the prefix.
func TestSubsystemPrefix(t *testing.T) {
	l, out, errBuf := captureLogger(LevelDebug, false)
	sub := l.WithSubsystem("cache")

	sub.Debug("HIT local x")
	assert.Contains(t, errBuf.String(), "cache: HIT local x")

	sub.Info("ready")
	assert.Contains(t, out.String(), "cache: ready")

}

// TestStdoutVsStderrRouting confirms Info/Output go to Stdout, Debug/Warn/Error go to Stderr.
func TestStdoutVsStderrRouting(t *testing.T) {
	l, out, errBuf := captureLogger(LevelDebug, false)

	l.Debug("stderr-debug")
	l.Warn("stderr-warn")
	l.Error("stderr-error")
	l.Info("stdout-info")
	l.Output("stdout-output")

	assert.Contains(t, errBuf.String(), "stderr-debug")

	assert.Contains(t, errBuf.String(), "stderr-warn")

	assert.Contains(t, errBuf.String(), "stderr-error")

	assert.Contains(t, out.String(), "stdout-info")

	assert.Contains(t, out.String(), "stdout-output")

}

// TestGHAAnnotations verifies GHA mode emits ::warning and ::error on stdout.
func TestGHAAnnotations(t *testing.T) {
	l, out, errBuf := captureLogger(LevelDebug, true)

	l.Warn("something went wrong")
	assert.Contains(t, out.String(), "::warning ::something went wrong")

	assert.Equal(t, 0, errBuf.Len())

	out.Reset()
	l.Error("fatal issue")
	assert.Contains(t, out.String(), "::error ::fatal issue")

}

// TestGHAFileAnnotations verifies WarnFile/ErrorFile include the file= attribute.
func TestGHAFileAnnotations(t *testing.T) {
	l, out, _ := captureLogger(LevelDebug, true)

	l.WarnFile("foo.go", "lint issue")
	assert.Contains(t, out.String(), "::warning file=foo.go::lint issue")

	out.Reset()
	l.ErrorFile("bar.go", "type error")
	assert.Contains(t, out.String(), "::error file=bar.go::type error")

}

// TestParseLevel confirms ParseLevel handles valid and invalid inputs.
func TestParseLevel(t *testing.T) {
	cases := []struct {
		input string
		want  Level
		ok    bool
	}{
		{"debug", LevelDebug, true},
		{"info", LevelInfo, true},
		{"warn", LevelWarn, true},
		{"warning", LevelWarn, true},
		{"error", LevelError, true},
		{"silent", LevelSilent, true},
		{"DEBUG", LevelDebug, true},
		{"bogus", LevelInfo, false},
	}
	for _, c := range cases {
		got, err := ParseLevel(c.input)
		assert.False(t, c.ok && err != nil)

		assert.False(t, !c.ok && err == nil)

		assert.False(t, c.ok && got != c.want)

	}
}

// TestDefaultLogger checks that the default logger is lazily initialized and
// can be replaced by Init.
func TestDefaultLogger(t *testing.T) {
	// Save and restore the global default so this test is hermetic.
	defaultMu.Lock()
	saved := defaultLogger
	defaultLogger = nil
	defaultMu.Unlock()
	defer func() {
		defaultMu.Lock()
		defaultLogger = saved
		defaultMu.Unlock()
	}()

	// Before Init, Default() returns a non-nil logger.
	d := Default()
	require.NotNil(t, d)

	// After Init, Default() returns the configured logger.
	out := &strings.Builder{}
	errBuf := &strings.Builder{}
	l := Init(Options{
		Level:  LevelWarn,
		Stdout: out,
		Stderr: errBuf,
	})
	assert.Equal(t, l, Default())

	// Info should be suppressed at LevelWarn.
	Info("ignored")
	assert.Equal(t, 0, out.Len())

	Warn("visible")
	assert.Contains(t, errBuf.String(), "visible")

}

// TestInitSubprocess verifies the subprocess logger routes every message to
// stderr and never emits GHA annotations on stdout, even when
// GITHUB_ACTIONS=true — stdout may be a protocol channel (e.g. GOCACHEPROG).
func TestInitSubprocess(t *testing.T) {
	// Save and restore the global default so this test is hermetic.
	defaultMu.Lock()
	saved := defaultLogger
	defaultMu.Unlock()
	defer func() {
		defaultMu.Lock()
		defaultLogger = saved
		defaultMu.Unlock()
	}()

	t.Setenv("GITHUB_ACTIONS", "true")

	// The subprocess logger writes through indirect writers; swap in pipes.
	origStdout, origStderr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	errR, errW, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout, os.Stderr = outW, errW
	defer func() { os.Stdout, os.Stderr = origStdout, origStderr }()

	l := InitSubprocess(LevelInfo)
	assert.Equal(t, l, Default())
	Debug("hidden at info")
	Info("info line")
	Warn("warn line")
	Output("output line")

	os.Stdout, os.Stderr = origStdout, origStderr
	outW.Close()
	errW.Close()
	outBytes, err := io.ReadAll(outR)
	require.NoError(t, err)
	errBytes, err := io.ReadAll(errR)
	require.NoError(t, err)

	// Nothing may reach stdout — it is the protocol channel.
	assert.Empty(t, string(outBytes))

	errStr := string(errBytes)
	assert.NotContains(t, errStr, "::warning")
	assert.Contains(t, errStr, "WARNING: warn line")
	assert.Contains(t, errStr, "info line")
	assert.Contains(t, errStr, "output line")
	assert.NotContains(t, errStr, "hidden at info")
}

// TestTrailingNewline checks that messages always end with exactly one newline.
func TestTrailingNewline(t *testing.T) {
	l, out, _ := captureLogger(LevelInfo, false)
	l.Info("no newline in message")
	s := out.String()
	assert.True(t, strings.HasSuffix(s, "\n"))

	assert.False(t, strings.HasSuffix(s, "\n\n"))

}
