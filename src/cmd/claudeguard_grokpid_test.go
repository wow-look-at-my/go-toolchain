package cmd

import (
	"os"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGrokNamedPID(t *testing.T) {
	t.Serial()
	t.Setenv("GROK_AGENT", "")
	t.Setenv(grokPIDEnv, strconv.Itoa(1234))
	assert.False(t, grokNamedPID(1234), "a pid var without GROK_AGENT is not grok")

	t.Setenv("GROK_AGENT", "0")
	assert.False(t, grokNamedPID(1234), "GROK_AGENT=0 is not under grok")

	t.Setenv("GROK_AGENT", "1")
	assert.True(t, grokNamedPID(1234))
	assert.False(t, grokNamedPID(1), "a different pid is not the named one")
	assert.True(t, harnessIsPID(1234))
	assert.False(t, harnessIsPID(1))

	t.Setenv(grokPIDEnv, "")
	assert.False(t, grokNamedPID(1234), "GROK_AGENT without a pid var is name-match only")
}

func TestHarnessIsPipeReaderRequiresAncestor(t *testing.T) {
	t.Serial()
	t.Setenv("GROK_AGENT", "1")
	t.Setenv(grokPIDEnv, strconv.Itoa(os.Getpid()))
	assert.False(t, harnessIsPipeReader("not-an-agent", os.Getpid()),
		"self is not an ancestor of the walk that starts at ppid")

	parent := os.Getppid()
	t.Setenv(grokPIDEnv, strconv.Itoa(parent))
	if runtime.GOOS == "windows" {
		// A native windows build links the no-process-tree stubs. The
		// APE that ships here reads cosmo's tree instead.
		assert.False(t, harnessIsPipeReader("not-an-agent", parent),
			"a build with no process tree can name no ancestor")
		return
	}
	assert.True(t, harnessIsPipeReader("not-an-agent", parent),
		"the parent named in GROK_AGENT_PID is the agent reading us")
}

func TestParseLsofPipeHandles(t *testing.T) {
	t.Serial()
	const out = "p367\n" +
		"f94\n" +
		"tPIPE\n" +
		"d0xebc7464f361551ca\n" +
		"n->0xda597fc854207746\n" +
		"f1\n" +
		"tCHR\n" +
		"d0x123\n" +
		"n/dev/ttys000\n" +
		"p34023\n" +
		"f1\n" +
		"tPIPE\n" +
		"d0xda597fc854207746\n" +
		"n->0xebc7464f361551ca\n"
	got := parseLsofPipeHandles(out)
	assert.Equal(t, uint64(0xda597fc854207746), got[367][0xebc7464f361551ca])
	assert.Equal(t, uint64(0xebc7464f361551ca), got[34023][0xda597fc854207746])
	_, ok := got[367][0x123]
	assert.False(t, ok, "a char device must not be recorded as a pipe")
}

func TestJoinPids(t *testing.T) {
	t.Serial()
	assert.Equal(t, "1,2,3", joinPids([]int{1, 2, 3}))
	assert.Empty(t, joinPids(nil))
}

func TestParseHexHandle(t *testing.T) {
	t.Serial()
	v, ok := parseHexHandle("0xebc7464f361551ca")
	assert.True(t, ok)
	assert.Equal(t, uint64(0xebc7464f361551ca), v)
	_, ok = parseHexHandle("")
	assert.False(t, ok)
	_, ok = parseHexHandle("zz")
	assert.False(t, ok)
}
