package cmd

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireCmdlineReader skips a test on a host that cannot read a process's
// command line. The classifier's fallback reads argv to separate a bare run
// from a captured one; on a platform with no such read there is nothing to
// assert. Windows is the no-op-classifier host (claudeguard_other.go).
func requireCmdlineReader(t *testing.T) {
	t.Helper()
	switch runtime.GOOS {
	case "linux", "cosmo", "darwin":
		return
	default:
		t.Skip("no process-command-line read on this host")
	}
}

// A host that will not show an ancestor's argv leaves the fallback with no
// evidence at all, and no evidence is not evidence of no pipe. Read that way
// the guard turns itself OFF wherever the read is refused, which is what let a
// real `| cat` through on darwin while every unit test still passed.
func TestUnidentifiedPeerFailsClosedWhenTheCommandLineIsUnreadable(t *testing.T) {
	t.Serial()
	requireWalkableAncestry(t)
	old := readCmdlineFunc
	readCmdlineFunc = func(int) ([]string, bool) { return nil, false }
	t.Cleanup(func() { readCmdlineFunc = old })

	_, piped, known := spawningPipeline()
	require.False(t, known, "a refused read must not report as a readable ancestry")
	require.False(t, piped)
	assert.Equal(t, sinkPipe, unidentifiedPeerSink(sinkPipe).kind,
		"blind is not acquitted: the guard convicts rather than switching itself off")
}

// The acquittal is what a READ shell with no capture buys, which is the bare
// run under a harness that 4a4979f exists to keep working.
func TestUnidentifiedPeerAcquitsOnAShellThatTypedNoPipe(t *testing.T) {
	t.Serial()
	requireWalkableAncestry(t)
	old := readCmdlineFunc
	readCmdlineFunc = func(int) ([]string, bool) { return []string{"/bin/sh", "-c", "go-toolchain"}, true }
	t.Cleanup(func() { readCmdlineFunc = old })

	cmd, piped, known := spawningPipeline()
	require.True(t, known)
	require.False(t, piped)
	require.NotEmpty(t, cmd, "the shell was read, so its text is the evidence")
	assert.Equal(t, sinkVisible, unidentifiedPeerSink(sinkPipe).kind)
}

// An ancestry holding no shell shows nothing either way, and on darwin that
// is every `| cat`: the reader is a sibling, and the FIFO probe there walks
// ancestors, so neither signal can see the pipe that is really present.
func TestUnidentifiedPeerFailsClosedWithNoShellToConsult(t *testing.T) {
	t.Serial()
	requireWalkableAncestry(t)
	old := readCmdlineFunc
	readCmdlineFunc = func(int) ([]string, bool) { return []string{"/usr/bin/some-harness"}, true }
	t.Cleanup(func() { readCmdlineFunc = old })

	cmd, piped, known := spawningPipeline()
	require.True(t, known)
	require.False(t, piped)
	require.Empty(t, cmd)
	assert.Equal(t, sinkPipe, unidentifiedPeerSink(sinkPipe).kind)
}

// requireWalkableAncestry skips a test that stubs the argv read, on a host
// where the walk has no parent to reach. Without a step the stub never runs,
// and the assertion passes on nothing: windows reported exactly that.
func requireWalkableAncestry(t *testing.T) {
	t.Helper()
	requireCmdlineReader(t)
	if parentPID() <= 1 {
		t.Skip("no ancestor to walk on this host")
	}
}

// The reported bug: a bare `go-toolchain` aborted saying its output was
// "piped into another command". Nothing in that command line is a pipe, and
// the guard had only guessed, because the reader sat outside its view.
func TestSpawningPipelineAcquitsACommandWithNoPipe(t *testing.T) {
	for _, script := range []string{
		"go-toolchain",
		"cd /repo && go-toolchain",
		"go-toolchain matrix",
		`echo "a|b" && go-toolchain`,
		"go-toolchain || echo failed",
		"go-toolchain 2>/dev/null",
	} {
		t.Run(script, func(t *testing.T) {
			assert.False(t, capturesStdout(script), "no stdout capture in this command line")
		})
	}
}

func TestSpawningPipelineConvictsARealCapture(t *testing.T) {
	for _, script := range []string{
		"go-toolchain | head -30",
		"go-toolchain|cat",
		"go-toolchain > out.log",
		"go-toolchain >> out.log",
		"go-toolchain 1> out.log",
		"out=$(go-toolchain)",
		"out=`go-toolchain`",
		"go-toolchain 2>&1 | tail",
	} {
		t.Run(script, func(t *testing.T) {
			assert.True(t, capturesStdout(script), "this command line does capture stdout")
		})
	}
}

// The whole point of reading the command line is that the message can QUOTE
// it. A sink carrying a command line must never say "another command".
func TestAbortMessageNamesTheActualCommandLine(t *testing.T) {
	msg := agentOutputMessage("grok build", outputSink{kind: sinkPipe, cmdline: "go-toolchain | head -30"}, nil)
	assert.Contains(t, msg, "go-toolchain | head -30")
	assert.NotContains(t, msg, "piped into another command")
}

func TestShellScriptFindsTheCommandString(t *testing.T) {
	script, ok := shellScript([]string{"/bin/sh", "-c", "go-toolchain | head"})
	require.True(t, ok)
	assert.Equal(t, "go-toolchain | head", script)

	_, ok = shellScript([]string{"/usr/bin/go-toolchain"})
	assert.False(t, ok, "a bare exec was handed no command string")

	_, ok = shellScript([]string{"node", "-c", "whatever"})
	assert.False(t, ok, "only a shell takes -c as a command string")
}

func TestIsShellMatchesTheUsualSpellings(t *testing.T) {
	for _, arg0 := range []string{"sh", "/bin/bash", "-bash", "zsh", "/usr/bin/dash", "busybox"} {
		assert.True(t, isShell(arg0), arg0)
	}
	for _, arg0 := range []string{"go-toolchain", "node", "python3", "shellcheck"} {
		assert.False(t, isShell(arg0), arg0)
	}
}

// readCmdline is the platform half. Whatever the host, this process's own
// argv must come back, or the walk above has nothing to read.
func TestReadCmdlineReadsThisProcess(t *testing.T) {
	requireCmdlineReader(t)
	argv, ok := readCmdline(selfPID())
	require.True(t, ok, "the guard cannot classify anything without this")
	require.NotEmpty(t, argv)
	assert.NotEmpty(t, argv[0])
}

// The walk must reach a real ancestor here, or spawningPipeline has nothing
// to read and every classification falls back to the guess this replaced.
func TestAncestorCmdlinesReachesRealProcesses(t *testing.T) {
	requireCmdlineReader(t)
	got, _ := ancestorCmdlines()
	require.NotEmpty(t, got, "no ancestor argv readable on this host")
	for _, argv := range got {
		assert.NotEmpty(t, argv[0])
	}
}

// The cases the peer cannot tell apart under grok-build: bare, and piped.
// Both have an unnameable FIFO reader, so the command line is the only thing
// that separates them.
func TestSpawningPipelineAnswersFromTheCommandLine(t *testing.T) {
	cmd, piped, _ := spawningPipeline()
	if piped {
		assert.NotEmpty(t, cmd, "a conviction must carry the text it convicted on")
		assert.True(t, capturesStdout(cmd))
		return
	}
	if cmd != "" {
		assert.False(t, capturesStdout(cmd), "acquitted, so the text must hold no capture")
	}
}

func TestParentLookupsAgree(t *testing.T) {
	requireCmdlineReader(t)
	ppid := parentPID()
	require.Positive(t, ppid)
	again, ok := parentOf(selfPID())
	require.True(t, ok)
	assert.Equal(t, ppid, again)
}

// An unidentified reader on a READ command line with no capture lets the run
// proceed: the acquittal the reported bug was missing. A conviction always
// says what it convicted on -- the captured command line, or the reason there
// was none to read.
func TestUnidentifiedPeerFallsBackToTheCommandLine(t *testing.T) {
	sink := unidentifiedPeerSink(sinkPipe)
	if sink.kind == sinkPipe {
		assert.True(t, sink.cmdline != "" || sink.detail != "",
			"a conviction must name the command line it read, or say why it read none")
		return
	}
	assert.Equal(t, sinkVisible, sink.kind)
}
