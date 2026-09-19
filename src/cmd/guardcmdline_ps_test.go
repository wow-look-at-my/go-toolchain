//go:build darwin || cosmo

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
)

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

// The capture the guard has to convict on lives inside the shell's command
// string, so a reader that split that string on spaces would see
// `out=$(go-toolchain` and nothing after it.
func TestParsePSCommandKeepsASubstitutionWhole(t *testing.T) {
	argv := parsePSCommand("/bin/sh -c cd /repo; out=$(go-toolchain 2>&1); echo done\n")
	require.Len(t, argv, 3)
	assert.Equal(t, "/bin/sh", argv[0])
	assert.Equal(t, "-c", argv[1])
	assert.Equal(t, "cd /repo; out=$(go-toolchain 2>&1); echo done", argv[2])

	script, ok := shellScript(argv)
	require.True(t, ok)
	assert.True(t, capturesStdout(script), "the capture is inside the command string")
}

func TestParsePSCommandOnAPlainExec(t *testing.T) {
	argv := parsePSCommand("/usr/local/bin/go-toolchain matrix\n")
	assert.Equal(t, []string{"/usr/local/bin/go-toolchain", "matrix"}, argv)
	_, ok := shellScript(argv)
	assert.False(t, ok, "no shell was handed a command string")
}

// A program that is not a shell keeps its arguments separate: the joining
// above is a property of the flag that takes a script, not of ps.
func TestParsePSCommandSplitsANonShell(t *testing.T) {
	argv := parsePSCommand("/usr/local/bin/go-toolchain matrix --targets cosmo")
	assert.Equal(t, []string{"/usr/local/bin/go-toolchain", "matrix", "--targets", "cosmo"}, argv)
}

func TestParsePSCommandOnEmptyOutput(t *testing.T) {
	assert.Empty(t, parsePSCommand("\n"))
	assert.Empty(t, parsePSCommand("   \n"), "ps printed no row")
}

// The reader itself, against a real process. This is the read the APE makes
// on a Mac, where /proc answers nothing.
func TestPSCmdlineReadsThisProcess(t *testing.T) {
	t.Serial()
	argv, ok := psCmdline(selfPID())
	require.True(t, ok, "the guard cannot classify anything without this")
	require.NotEmpty(t, argv)
	assert.NotEmpty(t, argv[0])

	_, ok = psCmdline(0)
	assert.False(t, ok, "zero is not a pid")
}

// The darwin host path a fat APE takes. A fake ps stands in for the tool,
// because a host with /proc would never reach it.
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
