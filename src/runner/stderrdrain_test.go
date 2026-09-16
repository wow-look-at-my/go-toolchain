package runner

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Enough stderr to outrun any pipe buffer, written with shell builtins so the
// script needs no coreutils on any host.
func floodScript(lines int) string {
	filler := strings.Repeat("x", 63)
	return fmt.Sprintf("i=0; while [ $i -lt %d ]; do printf '%%s\\n' %s >&2; i=$((i+1)); done; echo done", lines, filler)
}

// A caller that sets StderrWriter reads stdout itself and reaches Wait only
// after stdout ends. Nothing may hold stderr back until then: the child would
// block on a full pipe, so it would never exit, so stdout would never end.
func TestStderrDrainsWhileTheCallerReadsStdout(t *testing.T) {
	const lines = 8192
	var captured bytes.Buffer
	proc, err := Cmd("sh", "-c", floodScript(lines)).WithStderrWriter(&captured).Run(New())
	require.NoError(t, err)

	got := make(chan string, 1)
	go func() {
		out, _ := io.ReadAll(proc.Stdout())
		got <- string(out)
	}()

	select {
	case out := <-got:
		require.Contains(t, out, "done")
	case <-time.After(60 * time.Second):
		t.Fatal("stdout never ended: the child is blocked writing stderr nobody reads")
	}
	require.NoError(t, proc.Wait())
	require.Equal(t, lines*64, captured.Len())
}

// ReachablePackages runs go list quiet and reads only stdout. Stderr has to
// keep moving anyway, or the child never exits and that read never returns.
func TestQuietStdoutOnlyReadSurvivesAStderrFlood(t *testing.T) {
	const lines = 8192
	proc, err := Cmd("sh", "-c", floodScript(lines)).WithQuiet().Run(New())
	require.NoError(t, err)

	got := make(chan string, 1)
	go func() {
		out, _ := io.ReadAll(proc.Stdout())
		got <- string(out)
	}()

	select {
	case out := <-got:
		require.Contains(t, out, "done")
	case <-time.After(60 * time.Second):
		t.Fatal("stdout never ended: the child is blocked writing stderr nobody reads")
	}
	require.NoError(t, proc.Wait())
}

// A quiet caller still gets everything the child wrote to stderr.
func TestQuietCallerCanStillReadStderr(t *testing.T) {
	proc, err := Cmd("sh", "-c", "printf 'held\\n' >&2").WithQuiet().Run(New())
	require.NoError(t, err)
	captured, err := io.ReadAll(proc.Stderr())
	require.NoError(t, err)
	require.Equal(t, "held\n", string(captured))
	require.NoError(t, proc.Wait())
}

// Wait still reports the exit status, and stderr still arrives in full.
func TestStderrWriterGetsEverythingBeforeWaitReturns(t *testing.T) {
	var captured bytes.Buffer
	proc, err := Cmd("sh", "-c", "printf 'boom\\n' >&2; exit 3").WithStderrWriter(&captured).Run(New())
	require.NoError(t, err)
	require.Error(t, proc.Wait())
	require.Equal(t, "boom\n", captured.String())
}

// Without a StderrWriter, Wait keeps draining both streams as it always did.
func TestWaitStillDrainsBothStreamsWithoutAStderrWriter(t *testing.T) {
	var out bytes.Buffer
	cfg := Cmd("sh", "-c", "printf 'toout\\n'; printf 'toerr\\n' >&2")
	cfg.StdoutWriter = &out
	proc, err := cfg.Run(New())
	require.NoError(t, err)
	require.NoError(t, proc.Wait())
	require.Contains(t, out.String(), "toout")
}
