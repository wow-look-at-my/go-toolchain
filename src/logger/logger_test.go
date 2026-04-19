package logger

import (
	"strings"
	"testing"
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
	if errBuf.Len() != 0 {
		t.Errorf("Debug should be suppressed at LevelInfo, got: %q", errBuf.String())
	}

	l.Info("should appear on stdout")
	if !strings.Contains(out.String(), "should appear on stdout") {
		t.Errorf("Info not found in stdout: %q", out.String())
	}

	l.Warn("should appear on stderr")
	if !strings.Contains(errBuf.String(), "should appear on stderr") {
		t.Errorf("Warn not found in stderr: %q", errBuf.String())
	}

	errBuf.Reset()
	l.Error("also stderr")
	if !strings.Contains(errBuf.String(), "also stderr") {
		t.Errorf("Error not found in stderr: %q", errBuf.String())
	}
}

// TestDebugLevel verifies all messages appear when level is Debug.
func TestDebugLevel(t *testing.T) {
	l, out, errBuf := captureLogger(LevelDebug, false)

	l.Debug("debug msg")
	if !strings.Contains(errBuf.String(), "debug msg") {
		t.Errorf("Debug not found: %q", errBuf.String())
	}

	l.Info("info msg")
	if !strings.Contains(out.String(), "info msg") {
		t.Errorf("Info not found: %q", out.String())
	}
}

// TestWarnLevel verifies that at LevelWarn, Debug and Info are suppressed.
func TestWarnLevel(t *testing.T) {
	l, out, errBuf := captureLogger(LevelWarn, false)

	l.Debug("hidden debug")
	l.Info("hidden info")
	if errBuf.Len() != 0 {
		t.Errorf("Debug should be suppressed at LevelWarn, got: %q", errBuf.String())
	}
	if out.Len() != 0 {
		t.Errorf("Info should be suppressed at LevelWarn, got: %q", out.String())
	}

	l.Warn("visible warning")
	if !strings.Contains(errBuf.String(), "visible warning") {
		t.Errorf("Warn not found: %q", errBuf.String())
	}
}

// TestSilentLevel verifies that at LevelSilent only Output is visible.
func TestSilentLevel(t *testing.T) {
	l, out, errBuf := captureLogger(LevelSilent, false)

	l.Debug("no")
	l.Info("no")
	l.Warn("no")
	l.Error("no")

	if errBuf.Len() != 0 {
		t.Errorf("nothing should reach stderr at LevelSilent, got: %q", errBuf.String())
	}
	if out.Len() != 0 {
		t.Errorf("nothing should reach stdout at LevelSilent, got: %q", out.String())
	}

	// Output always prints.
	l.Output("real result")
	if !strings.Contains(out.String(), "real result") {
		t.Errorf("Output not found in stdout: %q", out.String())
	}
}

// TestOutputAlwaysPrints verifies Output ignores level filtering entirely.
func TestOutputAlwaysPrints(t *testing.T) {
	l, out, _ := captureLogger(LevelSilent, false)
	l.Output("unconditional")
	if !strings.Contains(out.String(), "unconditional") {
		t.Errorf("Output should always appear: %q", out.String())
	}
}

// TestSubsystemPrefix verifies that WithSubsystem prepends the prefix.
func TestSubsystemPrefix(t *testing.T) {
	l, out, errBuf := captureLogger(LevelDebug, false)
	sub := l.WithSubsystem("cache")

	sub.Debug("HIT local x")
	if !strings.Contains(errBuf.String(), "cache: HIT local x") {
		t.Errorf("subsystem prefix not found in debug: %q", errBuf.String())
	}

	sub.Info("ready")
	if !strings.Contains(out.String(), "cache: ready") {
		t.Errorf("subsystem prefix not found in info: %q", out.String())
	}
}

// TestStdoutVsStderrRouting confirms Info/Output go to Stdout, Debug/Warn/Error go to Stderr.
func TestStdoutVsStderrRouting(t *testing.T) {
	l, out, errBuf := captureLogger(LevelDebug, false)

	l.Debug("stderr-debug")
	l.Warn("stderr-warn")
	l.Error("stderr-error")
	l.Info("stdout-info")
	l.Output("stdout-output")

	if !strings.Contains(errBuf.String(), "stderr-debug") {
		t.Errorf("Debug should go to stderr: %q", errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "stderr-warn") {
		t.Errorf("Warn should go to stderr: %q", errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "stderr-error") {
		t.Errorf("Error should go to stderr: %q", errBuf.String())
	}
	if !strings.Contains(out.String(), "stdout-info") {
		t.Errorf("Info should go to stdout: %q", out.String())
	}
	if !strings.Contains(out.String(), "stdout-output") {
		t.Errorf("Output should go to stdout: %q", out.String())
	}
}

// TestGHAAnnotations verifies GHA mode emits ::warning and ::error on stdout.
func TestGHAAnnotations(t *testing.T) {
	l, out, errBuf := captureLogger(LevelDebug, true)

	l.Warn("something went wrong")
	if !strings.Contains(out.String(), "::warning ::something went wrong") {
		t.Errorf("GHA warning annotation not found in stdout: %q", out.String())
	}
	if errBuf.Len() != 0 {
		t.Errorf("GHA mode should not write to stderr: %q", errBuf.String())
	}

	out.Reset()
	l.Error("fatal issue")
	if !strings.Contains(out.String(), "::error ::fatal issue") {
		t.Errorf("GHA error annotation not found in stdout: %q", out.String())
	}
}

// TestGHAFileAnnotations verifies WarnFile/ErrorFile include the file= attribute.
func TestGHAFileAnnotations(t *testing.T) {
	l, out, _ := captureLogger(LevelDebug, true)

	l.WarnFile("foo.go", "lint issue")
	if !strings.Contains(out.String(), "::warning file=foo.go::lint issue") {
		t.Errorf("GHA file warning not found: %q", out.String())
	}

	out.Reset()
	l.ErrorFile("bar.go", "type error")
	if !strings.Contains(out.String(), "::error file=bar.go::type error") {
		t.Errorf("GHA file error not found: %q", out.String())
	}
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
		if c.ok && err != nil {
			t.Errorf("ParseLevel(%q): unexpected error: %v", c.input, err)
		}
		if !c.ok && err == nil {
			t.Errorf("ParseLevel(%q): expected error, got nil", c.input)
		}
		if c.ok && got != c.want {
			t.Errorf("ParseLevel(%q) = %d, want %d", c.input, got, c.want)
		}
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
	if d == nil {
		t.Fatal("Default() should never return nil")
	}

	// After Init, Default() returns the configured logger.
	out := &strings.Builder{}
	errBuf := &strings.Builder{}
	l := Init(Options{
		Level:  LevelWarn,
		Stdout: out,
		Stderr: errBuf,
	})
	if Default() != l {
		t.Error("Default() should return the logger set by Init")
	}

	// Info should be suppressed at LevelWarn.
	Info("ignored")
	if out.Len() != 0 {
		t.Errorf("Info should be suppressed at LevelWarn via Default: %q", out.String())
	}

	Warn("visible")
	if !strings.Contains(errBuf.String(), "visible") {
		t.Errorf("Warn should appear via Default: %q", errBuf.String())
	}
}

// TestTrailingNewline checks that messages always end with exactly one newline.
func TestTrailingNewline(t *testing.T) {
	l, out, _ := captureLogger(LevelInfo, false)
	l.Info("no newline in message")
	s := out.String()
	if !strings.HasSuffix(s, "\n") {
		t.Errorf("output should end with newline: %q", s)
	}
	if strings.HasSuffix(s, "\n\n") {
		t.Errorf("output should not have double newline: %q", s)
	}
}
