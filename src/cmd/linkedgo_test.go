package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The go link is the go command by name, so the go command started under it
// starts itself again under that name alone. Under any other name the go
// subcommand is what reaches it, and "go go" is a subcommand of nothing.
func TestSelfGoCommand(t *testing.T) {
	assert.Equal(t, []string{"/tmp/link/go"}, selfGoCommand("/tmp/link/go"))
	assert.Equal(t, []string{`C:\link\go.exe`}, selfGoCommand(`C:\link\go.exe`))
	assert.Equal(t, []string{"/opt/go-toolchain", "go"}, selfGoCommand("/opt/go-toolchain"))
	assert.Equal(t, []string{`D:\dist\go-toolchain.exe`, "go"}, selfGoCommand(`D:\dist\go-toolchain.exe`))
}

// Outside a pipeline run the NAME go is the host's own go command, never this
// binary. The go subcommand names this binary's go command outright, so it
// answers whoever asks.
func TestLinkedGoArgs(t *testing.T) {
	t.Setenv(linkedGoEnv, "")
	_, linked := LinkedGoArgs([]string{"go", "build"})
	assert.False(t, linked)
	assert.False(t, PipelineStartedGo())

	args, linked := LinkedGoArgs([]string{"go-toolchain", "go", "install", "example.com/x@v1"})
	assert.True(t, linked, "a hand-started go subcommand is the go command")
	assert.Equal(t, []string{"go", "install", "example.com/x@v1"}, args)

	_, linked = LinkedGoArgs([]string{"go-toolchain", "tool", "compile", "-V=full"})
	assert.True(t, linked, "a hand-started build tool is the linked tool")

	t.Setenv(linkedGoEnv, "1")
	assert.True(t, PipelineStartedGo())
	args, linked = LinkedGoArgs([]string{"/tmp/link/go", "build", "."})
	assert.True(t, linked)
	assert.Equal(t, []string{"/tmp/link/go", "build", "."}, args)

	args, linked = LinkedGoArgs([]string{`C:\link\go.exe`, "build", "."})
	assert.True(t, linked)
	assert.Equal(t, []string{`C:\link\go.exe`, "build", "."}, args)

	args, linked = LinkedGoArgs([]string{"go-toolchain", "go", "build", "."})
	assert.True(t, linked)
	assert.Equal(t, []string{"go", "build", "."}, args)

	args, linked = LinkedGoArgs([]string{"go-toolchain", "tool", "compile", "-V=full"})
	assert.True(t, linked)
	assert.Equal(t, []string{"go-toolchain", "tool", "compile", "-V=full"}, args)

	_, linked = LinkedGoArgs([]string{"go-toolchain", "matrix"})
	assert.False(t, linked)
}
