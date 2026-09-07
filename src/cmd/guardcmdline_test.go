package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
)

// requireCmdlineReader skips a host that cannot read a process's command
// line, which leaves the argv fallback nothing to assert.
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
// evidence, and no evidence is not evidence of a pipe. Convicting here was
// tried and reverted: it aborts a plain `go-toolchain` wherever the read is
// refused, so the tool cannot be run at all.
func TestUnidentifiedPeerRunsWhenTheCommandLineIsUnreadable(t *testing.T) {
	t.Serial()
	requireWalkableAncestry(t)
	old := readCmdlineFunc
	readCmdlineFunc = func(int) ([]string, bool) { return nil, false }
	t.Cleanup(func() { readCmdlineFunc = old })

	cmd, piped := spawningPipeline()
	require.False(t, piped)
	require.Empty(t, cmd)
	sink := unidentifiedPeerSink(sinkPipe)
	assert.Equal(t, sinkVisible, sink.kind,
		"an unreadable ancestry must not abort an ordinary run")
	assert.NotEmpty(t, sink.blind,
		"allowing without knowing is the one thing the guard must never do quietly")
}

// The banner is the whole point of the blind field: an allow the guard could
// not justify has to reach stderr, or it reads as an allow it checked.
func TestABlindAllowSaysSoOnStderr(t *testing.T) {
	t.Serial()
	var out strings.Builder
	oldOut, oldAgent, oldInspect := agentGuardOut, runningUnderAgentFn, inspectStdoutFn
	agentGuardOut = &out
	runningUnderAgentFn = func() (string, bool) { return "claude", true }
	inspectStdoutFn = func() outputSink {
		return outputSink{kind: sinkVisible, blind: "the reader could not be named"}
	}
	t.Cleanup(func() {
		agentGuardOut, runningUnderAgentFn, inspectStdoutFn = oldOut, oldAgent, oldInspect
	})

	guardAgainstAgentOutputCapture()
	assert.Contains(t, out.String(), "guard is BLIND")
	assert.Contains(t, out.String(), "the reader could not be named")
}

// A classification the guard actually made says nothing extra: the banner
// must mark the gap, never every run.
func TestAClassifiedVisibleSinkSaysNothing(t *testing.T) {
	t.Serial()
	var out strings.Builder
	oldOut, oldAgent, oldInspect := agentGuardOut, runningUnderAgentFn, inspectStdoutFn
	agentGuardOut = &out
	runningUnderAgentFn = func() (string, bool) { return "claude", true }
	inspectStdoutFn = func() outputSink { return outputSink{kind: sinkVisible} }
	t.Cleanup(func() {
		agentGuardOut, runningUnderAgentFn, inspectStdoutFn = oldOut, oldAgent, oldInspect
	})

	guardAgainstAgentOutputCapture()
	assert.Empty(t, out.String())
}

// The acquittal is what a READ shell with no capture buys, which is the bare
// run under a harness that 4a4979f exists to keep working.
func TestUnidentifiedPeerAcquitsOnAShellThatTypedNoPipe(t *testing.T) {
	t.Serial()
	requireWalkableAncestry(t)
	old := readCmdlineFunc
	readCmdlineFunc = func(int) ([]string, bool) { return []string{"/bin/sh", "-c", "go-toolchain"}, true }
	t.Cleanup(func() { readCmdlineFunc = old })

	cmd, piped := spawningPipeline()
	require.False(t, piped)
	require.NotEmpty(t, cmd, "the shell was read, so its text is the evidence")
	assert.Equal(t, sinkVisible, unidentifiedPeerSink(sinkPipe).kind)
}

// An ancestry holding no shell shows nothing either way. Nothing typed a
// pipe that anything here can see, so the run proceeds -- an exec with no
// shell above it is ordinary rather than suspicious.
func TestUnidentifiedPeerRunsWithNoShellToConsult(t *testing.T) {
	t.Serial()
	requireWalkableAncestry(t)
	old := readCmdlineFunc
	readCmdlineFunc = func(int) ([]string, bool) { return []string{"/usr/bin/some-harness"}, true }
	t.Cleanup(func() { readCmdlineFunc = old })

	cmd, piped := spawningPipeline()
	require.False(t, piped)
	require.Empty(t, cmd)
	assert.Equal(t, sinkVisible, unidentifiedPeerSink(sinkPipe).kind)
}

// ps prints a single string, so splitting it on spaces drops the pipe that
// decides the classification. The script after -c has to survive whole.
func TestParsePSCommandKeepsTheShellScriptWhole(t *testing.T) {
	argv := parsePSCommand("/bin/sh -c go-toolchain | head -30\n")
	require.Len(t, argv, 3)
	assert.Equal(t, []string{"/bin/sh", "-c", "go-toolchain | head -30"}, argv)

	script, ok := shellScript(argv)
	require.True(t, ok)
	assert.True(t, capturesStdout(script), "the pipe must survive the round trip")
}

func TestParsePSCommandOnAPlainExec(t *testing.T) {
	argv := parsePSCommand("/usr/local/bin/go-toolchain matrix\n")
	assert.Equal(t, []string{"/usr/local/bin/go-toolchain", "matrix"}, argv)
	_, ok := shellScript(argv)
	assert.False(t, ok, "no shell was handed a command string")
}

func TestParsePSCommandOnEmptyOutput(t *testing.T) {
	assert.Empty(t, parsePSCommand("\n"))
}

// The darwin host path a fat APE takes. A fake ps stands in for the tool,
// because this suite's own host has /proc and would never reach it.
func TestPSCmdlineReadsTheTool(t *testing.T) {
	t.Serial()
	// The stand-in is a `#!/bin/sh` script, which NT cannot start. The reader
	// it covers never runs there either: NT dispatches to procCmdline.
	if hostos.GOOS() == "windows" {
		t.Skip("no shebang execution on this host")
	}
	fake := filepath.Join(t.TempDir(), "ps")
	script := "#!/bin/sh\necho '/bin/sh -c go-toolchain > out.log'\n"
	require.NoError(t, os.WriteFile(fake, []byte(script), 0o755))

	old := psBin
	psBin = fake
	t.Cleanup(func() { psBin = old })

	argv, ok := psCmdline(4242)
	require.True(t, ok)
	assert.Equal(t, []string{"/bin/sh", "-c", "go-toolchain > out.log"}, argv)
}

// A sandbox that refuses ps answers nothing, which is no evidence rather than
// evidence of a capture.
func TestPSCmdlineReportsNothingWhenTheToolIsUnavailable(t *testing.T) {
	t.Serial()
	old := psBin
	psBin = filepath.Join(t.TempDir(), "absent-ps")
	t.Cleanup(func() { psBin = old })

	_, ok := psCmdline(4242)
	assert.False(t, ok)
	assert.Contains(t, probeDetail(), "absent-ps",
		"a missing ps must name itself: the banner otherwise reports only that no command line could be read, which sends the reader hunting a sandbox rule that does not exist")
}

// A refusal writes its reason on stderr, which is what separates a denied
// probe from a missing tool.
func TestPSCmdlineCarriesTheToolsOwnRefusal(t *testing.T) {
	t.Serial()
	if hostos.GOOS() == "windows" {
		t.Skip("no shebang execution on this host")
	}
	fake := filepath.Join(t.TempDir(), "ps")
	script := "#!/bin/sh\necho 'ps: Operation not permitted' >&2\nexit 1\n"
	require.NoError(t, os.WriteFile(fake, []byte(script), 0o755))

	old := psBin
	psBin = fake
	t.Cleanup(func() { psBin = old })

	_, ok := psCmdline(4242)
	assert.False(t, ok)
	assert.Contains(t, probeDetail(), "Operation not permitted")
}

// A ps that exits clean for a pid it cannot describe never failed to exec,
// so an exit-status message would name the wrong thing.
func TestPSCmdlineSaysWhenThereWasNoOutput(t *testing.T) {
	t.Serial()
	if hostos.GOOS() == "windows" {
		t.Skip("no shebang execution on this host")
	}
	fake := filepath.Join(t.TempDir(), "ps")
	require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	old := psBin
	psBin = fake
	t.Cleanup(func() { psBin = old })

	_, ok := psCmdline(4242)
	assert.False(t, ok)
	assert.Contains(t, probeDetail(), "printed nothing")
}

// requireWalkableAncestry skips a host with no parent to walk to, where the
// stub never runs and the assertion proves nothing.
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
	requireCmdlineReader(t)
	ppid := parentPID()
	require.Positive(t, ppid)
	again, ok := parentOf(selfPID())
	require.True(t, ok)
	assert.Equal(t, ppid, again)
}

// An unidentified reader lets the run proceed unless a command line was READ
// and shows a capture. A conviction therefore always carries that text, which
// is what the abort message quotes: nothing else may reach that slot.
func TestUnidentifiedPeerFallsBackToTheCommandLine(t *testing.T) {
	sink := unidentifiedPeerSink(sinkPipe)
	if sink.kind == sinkPipe {
		assert.NotEmpty(t, sink.cmdline, "a conviction must name the command line it read")
		assert.True(t, capturesStdout(sink.cmdline), "and that text must really capture stdout")
		return
	}
	assert.Equal(t, sinkVisible, sink.kind)
}

// The abort quotes `detail` as the thing stdout is piped INTO, so it has to
// be a command name. A sentence there renders as "piped into `the reader
// could not be named ...`", which is what shipped and what a reader then has
// to decode.
func TestPipeDetailIsACommandNameRatherThanAnExplanation(t *testing.T) {
	msg := agentOutputMessage("grok build", outputSink{kind: sinkPipe, detail: "cat"}, nil)
	assert.Contains(t, msg, "piped into `cat`")

	sink := unidentifiedPeerSink(sinkPipe)
	assert.Empty(t, sink.detail, "the fallback never puts prose where a command name is quoted")
}
