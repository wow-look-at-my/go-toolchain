package logx

import (
	"bufio"
	"fmt"
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
	if err != nil {
		t.Fatal(err)
	}
	tmpErr, err := os.CreateTemp(t.TempDir(), "stderr-*")
	if err != nil {
		t.Fatal(err)
	}

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

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

var tsLineRE = regexp.MustCompile(`^\d{2}:\d{2}:\d{2}\.\d{3} `)

func TestInstallPrependsTimestampToStderr(t *testing.T) {
	got := stripANSI(captureInstalled(t, func() {
		fmt.Fprintln(os.Stderr, "hello stderr")
	}))
	if !tsLineRE.MatchString(got) || !strings.Contains(got, "hello stderr\n") {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestInstallPrependsTimestampToStdout(t *testing.T) {
	got := stripANSI(captureInstalled(t, func() {
		fmt.Fprintln(os.Stdout, "hello stdout")
	}))
	if !tsLineRE.MatchString(got) || !strings.Contains(got, "hello stdout\n") {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestInstallHandlesPartialLines(t *testing.T) {
	// Simulates the step pattern: print prefix without newline, do work,
	// then print completion with newline — one logical line, one timestamp.
	got := stripANSI(captureInstalled(t, func() {
		fmt.Fprintf(os.Stdout, "⇒ Running tests with coverage...")
		fmt.Fprintf(os.Stdout, " done. 1.82s\n")
	}))
	re := regexp.MustCompile(`^\d{2}:\d{2}:\d{2}\.\d{3} ⇒ Running tests with coverage\.\.\. done\. 1\.82s\n$`)
	if !re.MatchString(got) {
		t.Fatalf("expected single timestamped line, got %q", got)
	}
}

func TestInstallTimestampsEachLineIndividually(t *testing.T) {
	got := stripANSI(captureInstalled(t, func() {
		fmt.Fprintf(os.Stderr, "one\ntwo\nthree\n")
	}))
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), got)
	}
	for _, line := range lines {
		if !tsLineRE.MatchString(line) {
			t.Fatalf("line missing timestamp: %q", line)
		}
	}
}

func TestLogfConvenience(t *testing.T) {
	got := stripANSI(captureInstalled(t, func() {
		Logf("hello %s", "world")
	}))
	re := regexp.MustCompile(`^\d{2}:\d{2}:\d{2}\.\d{3} hello world\n$`)
	if !re.MatchString(got) {
		t.Fatalf("unexpected: %q", got)
	}
}

func TestPrintfConvenience(t *testing.T) {
	got := stripANSI(captureInstalled(t, func() {
		Printf("status: %d", 42)
	}))
	re := regexp.MustCompile(`^\d{2}:\d{2}:\d{2}\.\d{3} status: 42\n$`)
	if !re.MatchString(got) {
		t.Fatalf("unexpected: %q", got)
	}
}

func TestPartialLineAtFlushIsEmitted(t *testing.T) {
	got := stripANSI(captureInstalled(t, func() {
		fmt.Fprintf(os.Stderr, "no newline yet")
		// No newline — Flush should still deliver it.
	}))
	re := regexp.MustCompile(`^\d{2}:\d{2}:\d{2}\.\d{3} no newline yet\n$`)
	if !re.MatchString(got) {
		t.Fatalf("expected flushed partial line, got %q", got)
	}
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
	lineRE := regexp.MustCompile(`^\d{2}:\d{2}:\d{2}\.\d{3} line-\d{3}-with-padding$`)
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if !lineRE.MatchString(line) {
			t.Fatalf("interleaved or malformed line: %q", line)
		}
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
	if got != "raw-hello\n" {
		t.Fatalf("expected raw output without timestamp, got %q", got)
	}
	_ = io.Discard
}
