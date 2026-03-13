package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/wow-look-at-my/testify/assert"
)

func TestColorPct(t *testing.T) {
	tests := []struct {
		pct      float32
		contains string
	}{
		{0, "\033[38;2;255;0;0m"},   // Red for 0%
		{100, "\033[38;2;0;255;0m"}, // Green for 100%
		{50, "50.0%"},               // Contains the percentage
	}

	for _, tc := range tests {
		result := colorPct(ColorPct{Pct: tc.pct})
		assert.Contains(t, result, tc.contains)
		assert.Contains(t, result, colorReset)
	}
}

func TestColorPctCustomFormat(t *testing.T) {
	result := colorPct(ColorPct{Pct: 50, Format: "%.0f%%"})
	assert.Contains(t, result, "50%")
}

func TestColorPctBoundaries(t *testing.T) {
	// Test that values outside 0-100 don't crash
	_ = colorPct(ColorPct{Pct: -10})
	_ = colorPct(ColorPct{Pct: 150})
}

func TestWarn(t *testing.T) {
	result := warn("test message")
	assert.Contains(t, result, "WARNING:")
	assert.Contains(t, result, "test message")
	assert.Contains(t, result, colorYellow)
	assert.Contains(t, result, colorReset)
}

func TestColorConstants(t *testing.T) {
	// Verify color constants have correct RGB values
	assert.Equal(t, "\033[38;2;0;255;0m", colorGreen)
	assert.Equal(t, "\033[38;2;255;0;0m", colorRed)
	assert.Equal(t, "\033[38;2;255;255;0m", colorYellow)
	assert.Equal(t, "\033[38;2;255;128;128m", colorFail)
	assert.Equal(t, colorGreen, colorPass)
}

// captureStdout runs f with stdout captured and returns the output.
func captureStdout(f func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	f()

	w.Close()
	var buf bytes.Buffer
	io.Copy(&buf, r)
	os.Stdout = old
	return buf.String()
}

func TestLogStepSilent(t *testing.T) {
	output := captureStdout(func() {
		s := logStep("go build")
		time.Sleep(10 * time.Millisecond)
		s.done()
	})
	assert.Contains(t, output, "==> go build...")
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
	assert.Contains(t, output, "==> go mod tidy...")
	// Should have newline after "..." (from noteOutput)
	assert.Contains(t, output, "...\n")
	// Done message should repeat the label on a new line with green "done."
	assert.Contains(t, output, "==> go mod tidy "+colorGreen+"done."+colorReset)
}

func TestLogStepFailed(t *testing.T) {
	output := captureStdout(func() {
		s := logStep("Running tests")
		s.noteOutput()
		s.failed()
	})
	assert.Contains(t, output, "==> Running tests...")
	assert.Contains(t, output, colorRed+"failed!"+colorReset)
	assert.Contains(t, output, colorDimCyan)
}

func TestLogStepFailedSilent(t *testing.T) {
	output := captureStdout(func() {
		s := logStep("go vet")
		s.failed()
	})
	assert.Contains(t, output, "==> go vet...")
	assert.Contains(t, output, colorRed+"failed!"+colorReset)
	assert.NotContains(t, output, "...\n")
}

func TestTimedLineWriter(t *testing.T) {
	var buf bytes.Buffer
	w := newTimedLineWriter(&buf)

	w.Write([]byte("go: downloading foo v1.0\n"))
	// Content is written immediately, but newline+timing is deferred
	assert.Equal(t, "go: downloading foo v1.0", buf.String())
	assert.NotContains(t, buf.String(), "\n")

	time.Sleep(10 * time.Millisecond)
	w.Write([]byte("go: downloading bar v2.0\n"))

	// First line should now be closed with timing
	output := buf.String()
	assert.Contains(t, output, "go: downloading foo v1.0 ")
	assert.Contains(t, output, colorDimCyan)

	// Flush closes the second line
	w.Flush()
	output = buf.String()
	assert.Contains(t, output, "go: downloading bar v2.0 ")
	// Should have exactly 2 lines
	lines := bytes.Count([]byte(output), []byte("\n"))
	assert.Equal(t, 2, lines)
}

func TestTimedLineWriterPartialWrites(t *testing.T) {
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
	var buf bytes.Buffer
	w := newTimedLineWriter(&buf)

	w.Write([]byte("unterminated"))
	w.Flush()

	output := buf.String()
	// Unterminated line should be flushed without timing
	assert.Equal(t, "unterminated", output)
}

func TestTimedLineWriterClosesOnPartialContent(t *testing.T) {
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
		s.noteOutput() // second call should be no-op
		s.done()
	})
	// Only one newline after "..." (not two)
	count := 0
	for i := 0; i < len(output)-3; i++ {
		if output[i:i+4] == "...\n" {
			count++
		}
	}
	assert.Equal(t, 1, count, "noteOutput should only print one newline")
}
