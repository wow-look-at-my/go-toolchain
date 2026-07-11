package logx

import (
	"bufio"
	"fmt"
	"github.com/stretchr/testify/require"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
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
	installOnce = sync.Once{}
	installed = false

	tmpOut, err := os.CreateTemp(t.TempDir(), "stdout-*")
	require.Nil(t, err)

	tmpErr, err := os.CreateTemp(t.TempDir(), "stderr-*")
	require.Nil(t, err)

	// Swap os.Stdout/Stderr to our temp files so Install captures those
	// as the "origStdout" / "origStderr" destinations.
	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout = tmpOut
	os.Stderr = tmpErr
	defer func() {
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

// durSuffixRE matches a line that ends with a space and a duration (e.g. " 0.00s").
var durSuffixRE = regexp.MustCompile(` \d+\.\d{2}s\n$`)

// durLineRE matches a single line (no trailing newline) that ends with a duration.
var durLineRE = regexp.MustCompile(` \d+\.\d{2}s$`)

func TestInstallAppendsDurationToStderr(t *testing.T) {
	got := stripANSI(captureInstalled(t, func() {
		fmt.Fprintln(os.Stderr, "hello stderr")
	}))
	require.True(t, strings.Contains(got, "hello stderr"))
	require.True(t, durSuffixRE.MatchString(got))
}

func TestInstallAppendsDurationToStdout(t *testing.T) {
	got := stripANSI(captureInstalled(t, func() {
		fmt.Fprintln(os.Stdout, "hello stdout")
	}))
	require.True(t, strings.Contains(got, "hello stdout"))
	require.True(t, durSuffixRE.MatchString(got))
}

func TestInstallHandlesPartialLines(t *testing.T) {
	// Simulates the step pattern: print prefix without newline, do work,
	// then print completion with newline. The line already ends with a
	// duration (1.82s) so drain() must NOT append another one.
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
	// No double duration — nothing should look like "5.73s \d+\.\d{2}s".
	doubleRE := regexp.MustCompile(`\d+\.\d{2}s \d+\.\d{2}s`)
	require.False(t, doubleRE.MatchString(got))
}

func TestInstallAppendsDurationToEachLine(t *testing.T) {
	got := stripANSI(captureInstalled(t, func() {
		fmt.Fprintf(os.Stderr, "one\ntwo\nthree\n")
	}))
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	require.Equal(t, 3, len(lines))

	for _, line := range lines {
		require.True(t, durLineRE.MatchString(line))
	}
}

func TestLogfConvenience(t *testing.T) {
	got := stripANSI(captureInstalled(t, func() {
		Logf("hello %s", "world")
	}))
	re := regexp.MustCompile(`^hello world \d+\.\d{2}s\n$`)
	require.True(t, re.MatchString(got))
}

func TestPrintfConvenience(t *testing.T) {
	got := stripANSI(captureInstalled(t, func() {
		Printf("status: %d", 42)
	}))
	re := regexp.MustCompile(`^status: 42 \d+\.\d{2}s\n$`)
	require.True(t, re.MatchString(got))
}

func TestPartialLineAtFlushIsEmitted(t *testing.T) {
	got := stripANSI(captureInstalled(t, func() {
		fmt.Fprintf(os.Stderr, "no newline yet")
		// No newline — Flush should still deliver it.
	}))
	re := regexp.MustCompile(`^no newline yet \d+\.\d{2}s\n$`)
	require.True(t, re.MatchString(got))
}

func TestConcurrentWritesDoNotInterleaveMidLine(t *testing.T) {
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

// Without Install(), Logf should write plainly to stderr — still useful.
func TestLogfWithoutInstallJustWritesToStderr(t *testing.T) {
	r, w, _ := os.Pipe()
	origErr := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = origErr }()

	done := make(chan string, 1)
	go func() {
		var buf strings.Builder
		br := bufio.NewReader(r)
		for {
			b, err := br.ReadByte()
			if err != nil {
				done <- buf.String()
				return
			}
			buf.WriteByte(b)
		}
	}()

	Logf("raw-hello")
	w.Close()

	got := <-done
	require.Equal(t, "raw-hello\n", got)

	_ = io.Discard
}
