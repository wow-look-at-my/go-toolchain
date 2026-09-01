package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestColorPct(t *testing.T) {
	t.Parallel()
	tests := []struct {
		pct      float32
		contains string
	}{
		{0, "\033[38;2;255;0;0m"},   // red at the bottom of the range
		{100, "\033[38;2;0;255;0m"}, // green at the top of the range
		{50, "50.0%"},               // contains the percentage
	}

	for _, tc := range tests {
		result := colorPct(ColorPct{Pct: tc.pct})
		assert.Contains(t, result, tc.contains)
		assert.Contains(t, result, colorReset)
	}
}

func TestColorPctCustomFormat(t *testing.T) {
	t.Parallel()
	result := colorPct(ColorPct{Pct: 50, Format: "%.0f%%"})
	assert.Contains(t, result, "50%")
}

func TestColorPctBoundaries(t *testing.T) {
	t.Parallel()
	// A percentage outside the range must not crash
	_ = colorPct(ColorPct{Pct: -10})
	_ = colorPct(ColorPct{Pct: 150})
}

func TestWarn(t *testing.T) {
	t.Parallel()
	result := warn("test message")
	assert.Contains(t, result, "WARNING:")
	assert.Contains(t, result, "test message")
	assert.Contains(t, result, colorYellow)
	assert.Contains(t, result, colorReset)
}

func TestColorConstants(t *testing.T) {
	t.Parallel()
	// Verify color constants have correct RGB values
	assert.Equal(t, "\033[38;2;0;255;0m", colorGreen)
	assert.Equal(t, "\033[38;2;255;0;0m", colorRed)
	assert.Equal(t, "\033[38;2;255;255;0m", colorYellow)
	assert.Equal(t, "\033[38;2;255;128;128m", colorFail)
	assert.Equal(t, colorGreen, colorPass)
}

// drainPipe reads r to EOF in the background. A read that starts only after
// the writer returns deadlocks on a full pipe, and NT pipes are small.
func drainPipe(r io.Reader) <-chan string {
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		done <- buf.String()
	}()
	return done
}

// captureStdout runs f with stdout captured and returns the output.
func captureStdout(f func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	done := drainPipe(r)

	f()

	w.Close()
	os.Stdout = old
	return <-done
}

// captureCombinedOutput runs f with stdout and stderr merged. logger.Warn
// routes to stderr locally and to a ::warning on stdout in CI.
func captureCombinedOutput(f func()) string {
	oldOut, oldErr := os.Stdout, os.Stderr
	r, w, _ := os.Pipe()
	os.Stdout = w
	os.Stderr = w
	done := drainPipe(r)

	f()

	w.Close()
	os.Stdout = oldOut
	os.Stderr = oldErr
	return <-done
}

func TestLogStepSilent(t *testing.T) {
	output := captureStdout(func() {
		s := logStep("go build")
		time.Sleep(10 * time.Millisecond)
		s.done()
	})
	assert.Contains(t, output, "⇒ go build...")
	assert.Contains(t, output, "done.")
	assert.Contains(t, output, colorGreen+"done."+colorReset)
	assert.Contains(t, output, colorDimCyan)
	// Should be on a single line (no newline between "..." and " done.")
	assert.NotContains(t, output, "...\n")
}

func TestLogStepNoisy(t *testing.T) {
	output := captureStdout(func() {
		s := logStep("go mod tidy")
		s.noteOutput()
		fmt.Println("go: downloading something")
		s.done()
	})
	assert.Contains(t, output, "⇒ go mod tidy...")
	// Should have newline after "..." (from noteOutput)
	assert.Contains(t, output, "...\n")
	// Done message should repeat the label on a new line with green "done."
	assert.Contains(t, output, "⇒ go mod tidy "+colorGreen+"done."+colorReset)
}

func TestLogStepFailed(t *testing.T) {
	output := captureStdout(func() {
		s := logStep("Running tests")
		s.noteOutput()
		s.failed()
	})
	assert.Contains(t, output, "⇒ Running tests...")
	assert.Contains(t, output, colorRed+"failed!"+colorReset)
	assert.Contains(t, output, colorDimCyan)
}

func TestLogStepFailedSilent(t *testing.T) {
	output := captureStdout(func() {
		s := logStep("go vet")
		s.failed()
	})
	assert.Contains(t, output, "⇒ go vet...")
	assert.Contains(t, output, colorRed+"failed!"+colorReset)
	assert.NotContains(t, output, "...\n")
}

// withTimedLineMinDuration lowers timedLineMinDuration for a single test, so
// it can exercise the slow-line path without sleeping for real.
func withTimedLineMinDuration(t *testing.T, d time.Duration) {
	t.Helper()
	old := timedLineMinDuration
	timedLineMinDuration = d
	t.Cleanup(func() { timedLineMinDuration = old })
}

func TestTimedLineWriterFastLinesOmitDuration(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := newTimedLineWriter(&buf)

	w.Write([]byte("go: downloading foo v1.0\n"))
	// Content is written immediately, but the newline is deferred.
	assert.Equal(t, "go: downloading foo v1.0", buf.String())
	assert.NotContains(t, buf.String(), "\n")

	w.Write([]byte("go: downloading bar v2.0\n"))
	// The earlier line closes with a bare newline: it was far too quick to time.
	output := buf.String()
	assert.Contains(t, output, "go: downloading foo v1.0\n")
	assert.NotContains(t, output, colorDimCyan)

	w.Flush()
	output = buf.String()
	assert.Equal(t, "go: downloading foo v1.0\ngo: downloading bar v2.0\n", output)
}

func TestTimedLineWriterSlowLinesGetDuration(t *testing.T) {
	withTimedLineMinDuration(t, 0)
	var buf bytes.Buffer
	w := newTimedLineWriter(&buf)

	w.Write([]byte("go: downloading foo v1.0\n"))
	// Content is written immediately, but newline+timing is deferred
	assert.Equal(t, "go: downloading foo v1.0", buf.String())
	assert.NotContains(t, buf.String(), "\n")

	time.Sleep(10 * time.Millisecond)
	w.Write([]byte("go: downloading bar v2.0\n"))

	// The earlier line should now be closed with timing
	output := buf.String()
	assert.Contains(t, output, "go: downloading foo v1.0 ")
	assert.Contains(t, output, colorDimCyan)

	// Flush closes the remaining line
	w.Flush()
	output = buf.String()
	assert.Contains(t, output, "go: downloading bar v2.0 ")
	lines := bytes.Count([]byte(output), []byte("\n"))
	assert.Equal(t, 2, lines)
}

func TestTimedLineWriterPartialWrites(t *testing.T) {
	withTimedLineMinDuration(t, 0)
	var buf bytes.Buffer
	w := newTimedLineWriter(&buf)

	// Simulate partial writes that combine into a full line
	w.Write([]byte("go: down"))
	w.Write([]byte("loading foo\n"))
	// Content is written, but newline+timing is deferred
	assert.Equal(t, "go: downloading foo", buf.String())
	assert.NotContains(t, buf.String(), "\n")

	w.Flush()
	output := buf.String()
	assert.Contains(t, output, "go: downloading foo ")
	assert.Contains(t, output, colorDimCyan)
}

func TestTimedLineWriterFlushPartial(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := newTimedLineWriter(&buf)

	w.Write([]byte("unterminated"))
	w.Flush()

	output := buf.String()
	// Unterminated line should be flushed without timing
	assert.Equal(t, "unterminated", output)
}

func TestTimedLineWriterClosesOnPartialContent(t *testing.T) {
	withTimedLineMinDuration(t, 0)
	var buf bytes.Buffer
	w := newTimedLineWriter(&buf)

	w.Write([]byte("line one\n"))
	assert.Equal(t, "line one", buf.String())
	assert.NotContains(t, buf.String(), "\n")

	time.Sleep(10 * time.Millisecond)
	// Partial content (no newline) should close the previous line
	w.Write([]byte("partial"))
	output := buf.String()
	assert.Contains(t, output, "line one ")
	assert.Contains(t, output, colorDimCyan)
	assert.Contains(t, output, "\n")

	w.Flush()
	output = buf.String()
	assert.Contains(t, output, "partial")
}

func TestLogStepNoteOutputIdempotent(t *testing.T) {
	output := captureStdout(func() {
		s := logStep("test")
		s.noteOutput()
		s.noteOutput() // the repeat call should be a no-op
		s.done()
	})
	// A single newline after the ellipsis, never a repeat
	count := 0
	for i := 0; i < len(output)-3; i++ {
		if output[i:i+4] == "...\n" {
			count++
		}
	}
	assert.Equal(t, 1, count, "noteOutput should only print one newline")
}
