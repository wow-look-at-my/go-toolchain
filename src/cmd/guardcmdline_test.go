package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	argv, ok := readCmdline(selfPID())
	require.True(t, ok, "the guard cannot classify anything without this")
	require.NotEmpty(t, argv)
	assert.NotEmpty(t, argv[0])
}

// ps prints a whole command line on one line, so a shell's command string --
// which holds the spaces -- has to be recovered before capturesStdout can read
// it. This is the host half every non-linux reader answers through.
func TestPSArgvRejoinsAShellCommandString(t *testing.T) {
	got, ok := psArgv("/bin/sh -c go-toolchain | head -30")
	require.True(t, ok)
	require.Len(t, got, 3)
	assert.Equal(t, []string{"/bin/sh", "-c", "go-toolchain | head -30"}, got)
	assert.True(t, capturesStdout(got[2]), "the rejoined text is what the walk must convict on")
}

// The reported bug's shape: a bare command must come back acquittable.
func TestPSArgvAcquitsABareCommand(t *testing.T) {
	got, ok := psArgv("/bin/bash -lc cd /repo && go-toolchain")
	require.True(t, ok)
	require.Len(t, got, 3)
	assert.Equal(t, "cd /repo && go-toolchain", got[2])
	assert.False(t, capturesStdout(got[2]))
}

// A bundled flag such as -lc takes the command string, and ps cannot say where
// the string ends, so the whole tail is it.
func TestPSArgvHandlesABundledFlag(t *testing.T) {
	got, ok := psArgv("zsh -lc out=$(go-toolchain)")
	require.True(t, ok)
	require.Len(t, got, 3)
	assert.Equal(t, "out=$(go-toolchain)", got[2])
}

// A non-shell command line is handed back as ps printed it: nothing there needs
// rejoining, and a guess would corrupt an argv the walk then reads.
func TestPSArgvLeavesANonShellAlone(t *testing.T) {
	got, ok := psArgv("/usr/bin/go-toolchain matrix")
	require.True(t, ok)
	assert.Equal(t, []string{"/usr/bin/go-toolchain", "matrix"}, got)
}

// A shell with no command string is not a shell this walk can read, so no
// rejoining happens and the fields stay as printed.
func TestPSArgvLeavesAShellWithoutACommandString(t *testing.T) {
	got, ok := psArgv("/bin/sh -i")
	require.True(t, ok)
	assert.Equal(t, []string{"/bin/sh", "-i"}, got)
}

// An empty line is no process, not an empty argv: the caller must fall through
// to acquittal rather than convict on nothing.
func TestPSArgvRejectsAnEmptyLine(t *testing.T) {
	_, ok := psArgv("   \n")
	assert.False(t, ok)
}

// The walk must reach a real ancestor here, or spawningPipeline has nothing
// to read and every classification falls back to the guess this replaced.
func TestAncestorCmdlinesReachesRealProcesses(t *testing.T) {
	got := ancestorCmdlines()
	require.NotEmpty(t, got, "no ancestor argv readable on this host")
	for _, argv := range got {
		assert.NotEmpty(t, argv[0])
	}
}

// The cases the peer cannot tell apart under grok-build: bare, and piped.
// Both have an unnameable FIFO reader, so the command line is the only thing
// that separates them.
func TestSpawningPipelineAnswersFromTheCommandLine(t *testing.T) {
	cmd, piped := spawningPipeline()
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
	ppid := parentPID()
	require.Positive(t, ppid)
	again, ok := parentOf(selfPID())
	require.True(t, ok)
	assert.Equal(t, ppid, again)
}

// An unidentified reader on a command line with no capture must let the run
// proceed. That is the acquittal the reported bug was missing.
func TestUnidentifiedPeerFallsBackToTheCommandLine(t *testing.T) {
	sink := unidentifiedPeerSink(sinkPipe)
	if sink.kind == sinkPipe {
		assert.NotEmpty(t, sink.cmdline, "a conviction must name the command line it read")
		return
	}
	assert.Equal(t, sinkVisible, sink.kind)
}
