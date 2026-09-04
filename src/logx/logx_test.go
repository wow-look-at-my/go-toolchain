package logx

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// captureInstalled runs fn with Install() active, replacing the underlying
// destination files with a temp file so we can inspect timestamped output.
// It swaps the orig* package-level fields before/after Install.
//
// We can't share global state across parallel tests, so these tests run
// serially (t.Parallel is NOT called).
func captureInstalled(t *testing.T, fn func()) string {
	t.Helper()
	// Reset installation state so we can call Install again inside this test.
	// drainedWG resets with the rest: an earlier test that skipped Flush leaves
	// its drainers blocked on a pipe nobody closes, and a Wait that also counts
	// them never returns -- the package then dies on its timeout rather than on
	// the test that actually broke.
	installOnce = sync.Once{}
	installed = false
	drainedWG = sync.WaitGroup{}

	tmpOut, err := os.CreateTemp(t.TempDir(), "stdout-*")
	require.Nil(t, err)

	tmpErr, err := os.CreateTemp(t.TempDir(), "stderr-*")
	require.Nil(t, err)

	// Swap os.Stdout/Stderr for temp files so Install captures the originals.
	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout = tmpOut
	os.Stderr = tmpErr
	// Flush is what releases drainedWG, and it is package-level: a test that
	// fails or panics inside fn leaves its drainers blocked on a pipe nobody
	// closes, and every later Flush waits on them until the package hits its
	// timeout. Flush is safe to repeat, so the explicit call below still reads
	// as the point the output is complete.
	defer func() {
		Flush()
		os.Stdout = origOut
		os.Stderr = origErr
	}()

	Install()
	fn()
	Flush()

	tmpOut.Close()
	tmpErr.Close()

	outBytes, _ := os.ReadFile(tmpOut.Name())
	errBytes, _ := os.ReadFile(tmpErr.Name())
	return string(outBytes) + string(errBytes)
}

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

// durSuffixRE matches a line that ends with a space and a duration.
var durSuffixRE = regexp.MustCompile(` \d+\.\d{2}s\n$`)

// durLineRE matches a single line (no trailing newline) that ends with a duration.
var durLineRE = regexp.MustCompile(` \d+\.\d{2}s$`)

// withMinDuration lowers minDurationToShow for a single test, so it can
// exercise the slow-line path without sleeping for real.
func withMinDuration(t *testing.T, d time.Duration) {
	t.Helper()
	old := minDurationToShow
	minDurationToShow = d
	t.Cleanup(func() { minDurationToShow = old })
}

func TestInstallOmitsDurationOnFastStderrLine(t *testing.T) {
	got := stripANSI(captureInstalled(t, func() {
		fmt.Fprintln(os.Stderr, "hello stderr")
	}))
	require.Equal(t, "hello stderr\n", got)
}

func TestInstallOmitsDurationOnFastStdoutLine(t *testing.T) {
	got := stripANSI(captureInstalled(t, func() {
		fmt.Fprintln(os.Stdout, "hello stdout")
	}))
	require.Equal(t, "hello stdout\n", got)
}

func TestInstallAppendsDurationToSlowStderrLine(t *testing.T) {
	withMinDuration(t, 0)
	got := stripANSI(captureInstalled(t, func() {
		fmt.Fprintln(os.Stderr, "hello stderr")
	}))
	require.True(t, strings.Contains(got, "hello stderr"))
	require.True(t, durSuffixRE.MatchString(got))
}

func TestInstallAppendsDurationToSlowStdoutLine(t *testing.T) {
	withMinDuration(t, 0)
	got := stripANSI(captureInstalled(t, func() {
		fmt.Fprintln(os.Stdout, "hello stdout")
	}))
	require.True(t, strings.Contains(got, "hello stdout"))
	require.True(t, durSuffixRE.MatchString(got))
}

func TestInstallHandlesPartialLines(t *testing.T) {
	// The prefix prints without a newline, then the completion prints
	// its own duration; drain() must not append a further suffix.
	got := stripANSI(captureInstalled(t, func() {
		fmt.Fprintf(os.Stdout, "⇒ Running tests with coverage...")
		fmt.Fprintf(os.Stdout, " done. 1.82s\n")
	}))
	re := regexp.MustCompile(`^⇒ Running tests with coverage\.\.\. done\. 1\.82s\n$`)
	require.True(t, re.MatchString(got))
}

func TestInstallSkipsAlreadyTimedLines(t *testing.T) {
	// step.finish writes lines that already end with a fmtDuration suffix.
	// drain() should detect that and leave the line alone.
	got := stripANSI(captureInstalled(t, func() {
		fmt.Fprintln(os.Stderr, "    vet: type-check 5.73s")
		fmt.Fprintln(os.Stdout, "⇒ go vet ./... done. 28.46s")
	}))
	require.True(t, strings.Contains(got, "    vet: type-check 5.73s\n"))
	require.True(t, strings.Contains(got, "⇒ go vet ./... done. 28.46s\n"))
	// No double duration — nothing should carry a duration right after a duration.
	doubleRE := regexp.MustCompile(`\d+\.\d{2}s \d+\.\d{2}s`)
	require.False(t, doubleRE.MatchString(got))
}

func TestInstallOmitsDurationOnEachFastLine(t *testing.T) {
	got := stripANSI(captureInstalled(t, func() {
		fmt.Fprintf(os.Stderr, "one\ntwo\nthree\n")
	}))
	require.Equal(t, "one\ntwo\nthree\n", got)
}

func TestInstallAppendsDurationToEachSlowLine(t *testing.T) {
	withMinDuration(t, 0)
	got := stripANSI(captureInstalled(t, func() {
		fmt.Fprintf(os.Stderr, "one\ntwo\nthree\n")
	}))
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	require.Equal(t, 3, len(lines))

	for _, line := range lines {
		require.True(t, durLineRE.MatchString(line))
	}
}

func TestPartialLineAtFlushIsEmittedWithoutDurationWhenFast(t *testing.T) {
	got := stripANSI(captureInstalled(t, func() {
		fmt.Fprintf(os.Stderr, "no newline yet")
		// No newline — Flush should still deliver it.
	}))
	require.Equal(t, "no newline yet\n", got)
}

func TestPartialLineAtFlushIsEmittedWithDurationWhenSlow(t *testing.T) {
	withMinDuration(t, 0)
	got := stripANSI(captureInstalled(t, func() {
		fmt.Fprintf(os.Stderr, "no newline yet")
		// No newline — Flush should still deliver it.
	}))
	re := regexp.MustCompile(`^no newline yet \d+\.\d{2}s\n$`)
	require.True(t, re.MatchString(got))
}

func TestConcurrentWritesDoNotInterleaveMidLine(t *testing.T) {
	withMinDuration(t, 0)
	got := stripANSI(captureInstalled(t, func() {
		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				fmt.Fprintf(os.Stderr, "line-%03d-with-padding\n", i)
			}(i)
		}
		wg.Wait()
	}))
	lineRE := regexp.MustCompile(`^line-\d{3}-with-padding \d+\.\d{2}s$`)
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		require.True(t, lineRE.MatchString(line))
	}
}
