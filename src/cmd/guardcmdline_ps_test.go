//go:build darwin || cosmo

package cmd

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The capture the guard has to convict on lives inside the shell's command
// string, so a reader that split that string on spaces would see `out=$(go-toolchain)`
// and nothing after it.
func TestParsePSArgsKeepsTheCommandStringWhole(t *testing.T) {
	argv, ok := parsePSArgs("/bin/sh -c cd /repo; out=$(go-toolchain 2>&1); echo done\n")
	require.True(t, ok)
	require.Len(t, argv, 3)
	assert.Equal(t, "/bin/sh", argv[0])
	assert.Equal(t, "-c", argv[1])
	assert.Equal(t, "cd /repo; out=$(go-toolchain 2>&1); echo done", argv[2])

	script, ok := shellScript(argv)
	require.True(t, ok)
	assert.True(t, capturesStdout(script), "the capture is inside the command string")
}

// A program that is not a shell keeps its arguments separate: the joining
// above is a property of the flag that takes a script, not of ps.
func TestParsePSArgsSplitsANonShell(t *testing.T) {
	argv, ok := parsePSArgs("/usr/local/bin/go-toolchain matrix --targets cosmo")
	require.True(t, ok)
	assert.Equal(t, []string{"/usr/local/bin/go-toolchain", "matrix", "--targets", "cosmo"}, argv)

	_, ok = parsePSArgs("   \n")
	assert.False(t, ok, "ps printed no row")
}

// The reader itself, against a real process. This is the read the APE makes
// on a Mac, where /proc answers nothing.
func TestReadCmdlinePSReadsThisProcess(t *testing.T) {
	argv, ok := readCmdlinePS(os.Getpid())
	require.True(t, ok, "the guard cannot classify anything without this")
	require.NotEmpty(t, argv)
	assert.NotEmpty(t, argv[0])

	_, ok = readCmdlinePS(0)
	assert.False(t, ok, "zero is not a pid")
}
