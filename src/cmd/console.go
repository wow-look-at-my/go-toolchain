package cmd

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/summary"
)

const colorReset = "\033[0m"
const colorYellow = "\033[38;2;255;255;0m"
const colorGreen = "\033[38;2;0;255;0m"
const colorRed = "\033[38;2;255;0;0m"
const colorPass = colorGreen
const colorFail = "\033[38;2;255;128;128m"    // softer red for readability
const colorDimCyan = "\033[38;2;100;160;160m" // dark greyish-cyan for durations

type ColorPct struct {
	Pct    float32
	Format string
}

// hslToRGB converts HSL to RGB. h is in degrees [0,360), s and l are [0,1].
func hslToRGB(h, s, l float64) (r, g, b uint8) {
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2

	var r1, g1, b1 float64
	switch {
	case h < 60:
		r1, g1, b1 = c, x, 0
	case h < 120:
		r1, g1, b1 = x, c, 0
	case h < 180:
		r1, g1, b1 = 0, c, x
	case h < 240:
		r1, g1, b1 = 0, x, c
	case h < 300:
		r1, g1, b1 = x, 0, c
	default:
		r1, g1, b1 = c, 0, x
	}

	return uint8((r1 + m) * 255), uint8((g1 + m) * 255), uint8((b1 + m) * 255)
}

// colorPct formats a percentage with color based on value (red=0%, green=100%)
// Uses HSL hue rotation: 0° (red) → 60° (yellow) → 120° (green)
func colorPct(p ColorPct) string {
	format := p.Format
	if format == "" {
		format = "%6.1f%%"
	}
	// Map 0-100% to hue 0-120° (red to green through yellow)
	hue := float64(p.Pct) * 1.2 // 0% = 0°, 100% = 120°
	r, g, b := hslToRGB(hue, 1.0, 0.5)
	return fmt.Sprintf("\033[38;2;%d;%d;%dm"+format+colorReset, r, g, b, p.Pct)
}

// warn formats a warning message in yellow
func warn(msg string) string {
	return colorYellow + "WARNING: " + msg + colorReset
}

// isGHA reports whether we are running inside GitHub Actions.
func isGHA() bool {
	return os.Getenv("GITHUB_ACTIONS") == "true"
}

// logWarning prints a warning: a ::warning annotation in GHA, yellow text otherwise.
// file is optional, for GHA file annotations.
func logWarning(file, msg string) {
	if file != "" {
		logger.WarnFile(file, "%s", msg)
	} else {
		logger.Warn("%s", msg)
	}
}

// logError prints an error: a ::error annotation in GHA, red text otherwise.
// file is optional, for GHA file annotations.
func logError(file, msg string) {
	if file != "" {
		logger.ErrorFile(file, "%s", msg)
	} else {
		logger.Error("%s", msg)
	}
}

// pipelineTimeline records pipeline step times; nil until InitTimeline runs.
var pipelineTimeline *summary.Timeline

// InitTimeline creates the global pipeline timeline, anchored to now.
func InitTimeline() {
	pipelineTimeline = summary.NewTimeline()
}

// GetTimeline returns the global pipeline timeline (may be nil).
func GetTimeline() *summary.Timeline {
	return pipelineTimeline
}

// step tracks progress for a build step: prints "⇒ label..." then " done. (Xs)".
// A noisy step's done message starts on a new line with the label repeated.
// Sub-steps (logSubStep) print indented "    label Xs" instead.
type step struct {
	label  string
	thread string
	start  time.Time
	noisy  bool
	sub    bool // sub-step: indented output, no "⇒" prefix
	once   sync.Once
}

// logStep prints "⇒ label..." without a newline and returns a step
// that can be finished later with done(). Records on the "main" thread.
func logStep(label string) *step {
	return logStepOn(label, "main")
}

// logStepOn is like logStep but records on the given thread.
func logStepOn(label, thread string) *step {
	fmt.Fprintf(os.Stdout, "⇒ %s...", label)
	if activeWatchdog != nil {
		activeWatchdog.setStep(label)
	}
	return &step{label: label, thread: thread, start: time.Now()}
}

// logSubStep creates a sub-step that prints "    label Xs" only on completion,
// for recording sub-phases (e.g. vet phases) with their own timing.
func logSubStep(label, thread string) *step {
	if activeWatchdog != nil {
		activeWatchdog.setStep(label)
	}
	return &step{label: label, thread: thread, start: time.Now(), sub: true}
}

// noteOutput marks visible output during this step. On the first call it
// prints a newline so subprocess output starts on its own line.
func (s *step) noteOutput() {
	s.once.Do(func() {
		s.noisy = true
		fmt.Fprintln(os.Stdout) // finish the "..." line before subprocess output
	})
}

// fmtDuration formats a duration as dark greyish-cyan without parentheses.
func fmtDuration(d time.Duration) string {
	return fmt.Sprintf("%s%.2fs%s", colorDimCyan, d.Seconds(), colorReset)
}

// finish prints the completion message with elapsed time and a status word.
func (s *step) finish(status string) {
	end := time.Now()
	d := end.Sub(s.start)
	if s.sub {
		fmt.Fprintf(os.Stdout, "    %s %s\n", s.label, fmtDuration(d))
	} else if s.noisy {
		fmt.Fprintf(os.Stdout, "⇒ %s %s %s\n", s.label, status, fmtDuration(d))
	} else {
		fmt.Fprintf(os.Stdout, " %s %s\n", status, fmtDuration(d))
	}

	if activeWatchdog != nil {
		activeWatchdog.clearStep()
	}

	// Record to the pipeline timeline if initialized
	if pipelineTimeline != nil {
		failed := strings.Contains(status, "failed")
		pipelineTimeline.Record(s.label, s.thread, s.start, end, failed)
	}
}

// done prints a green "done." completion message with elapsed time.
func (s *step) done() {
	s.finish(colorGreen + "done." + colorReset)
}

// failed prints a red "failed!" completion message with elapsed time.
func (s *step) failed() {
	s.finish(colorRed + "failed!" + colorReset)
}

// timedLineWriter wraps a writer and appends elapsed time to each line. A line's
// newline is deferred until the next content arrives, so its duration reflects
// the wall-clock gap until the next line appeared.
type timedLineWriter struct {
	target      io.Writer
	buf         bytes.Buffer
	awaitingEnd bool      // wrote line content, waiting to close with timing
	lineEnd     time.Time // when the line content was written
}

// newTimedLineWriter creates a writer that appends elapsed time to each line.
func newTimedLineWriter(target io.Writer) *timedLineWriter {
	return &timedLineWriter{target: target}
}

func (w *timedLineWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.buf.Write(p)
	for {
		line, err := w.buf.ReadBytes('\n')
		if err != nil { // no newline found — put remainder back
			// If we have pending partial content and an open line, close it
			if len(line) > 0 && w.awaitingEnd {
				w.closeLine()
			}
			w.buf.Write(line)
			break
		}
		// Complete line found. Close any previous open line first.
		if w.awaitingEnd {
			w.closeLine()
		}
		// Write this line's content without the trailing \n
		trimmed := bytes.TrimRight(line, "\n")
		w.target.Write(trimmed)
		w.lineEnd = time.Now()
		w.awaitingEnd = true
	}
	return n, nil
}

// closeLine appends " <elapsed>\n" to finish the current open line.
func (w *timedLineWriter) closeLine() {
	fmt.Fprintf(w.target, " %s\n", fmtDuration(time.Since(w.lineEnd)))
	w.awaitingEnd = false
}

// Flush closes any open line and writes remaining buffered content.
func (w *timedLineWriter) Flush() {
	if w.awaitingEnd {
		w.closeLine()
	}
	if w.buf.Len() > 0 {
		w.target.Write(w.buf.Bytes())
		w.buf.Reset()
	}
}
