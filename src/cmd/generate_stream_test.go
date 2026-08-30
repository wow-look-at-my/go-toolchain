package cmd

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A directive's output must reach the console while the command is still
// running. It used to be buffered until exit, which hid it from the output
// watchdog (it monitors stdout and stderr) and made every slow directive -- `go run
// <tool>@latest` downloading modules, say -- print a repeating STALLED banner
// for its whole duration.
//
// The helper announces itself and then waits, bounded, for the test to react to
// that announcement. Buffered output means the reaction never arrives in time
// and the helper exits with a failure, so this fails on regression rather than
// racing a wall-clock deadline.
func TestExecuteDirectiveStreamsOutputWhileRunning(t *testing.T) {
	requireShebangHelper(t)
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.go")
	require.NoError(t, os.WriteFile(testFile, []byte("package main\n"), 0644))

	sentinel := filepath.Join(dir, "reader-saw-it")
	helper := filepath.Join(dir, "announce-then-wait")
	script := "#!/bin/sh\n" +
		"echo streaming-marker\n" +
		"i=0\n" +
		"while [ \"$i\" -lt 100 ]; do\n" +
		"  [ -f " + sentinel + " ] && exit 0\n" +
		"  sleep 0.1\n" +
		"  i=$((i+1))\n" +
		"done\n" +
		"exit 1\n"
	require.NoError(t, os.WriteFile(helper, []byte(script), 0755))

	r, w, err := os.Pipe()
	require.NoError(t, err)
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()

	go func() {
		br := bufio.NewReader(r)
		for {
			line, err := br.ReadString('\n')
			if strings.Contains(line, "streaming-marker") {
				_ = os.WriteFile(sentinel, nil, 0644)
			}
			if err != nil {
				return
			}
		}
	}()

	d := generateDirective{File: testFile, Line: 1, Command: helper}
	runErr := executeDirective(d, false)

	os.Stdout = old
	w.Close()
	require.NoError(t, runErr, "output was not visible until the command exited")
}

func TestStreamPrefixWriter(t *testing.T) {
	var w streamPrefixWriter

	// A complete line is emitted on arrival; a partial line waits for Flush.
	out := captureStdout(func() {
		n, err := w.Write([]byte("first\nsecond"))
		require.NoError(t, err)
		assert.Equal(t, len("first\nsecond"), n)
	})
	assert.Equal(t, "\t> first\n", out)

	out = captureStdout(w.Flush)
	assert.Equal(t, "\t> second\n", out)

	// Flush with nothing pending emits nothing, and CRLF loses the CR.
	out = captureStdout(func() {
		w.Flush()
		_, _ = w.Write([]byte("crlf\r\n"))
	})
	assert.Equal(t, "\t> crlf\n", out)

	// The same shape prefixOutput produces for the buffered (quiet) path.
	out = captureStdout(func() { _, _ = w.Write([]byte("a\nb\n")) })
	assert.Equal(t, prefixOutput("a\nb\n"), out)
}
