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

// Outside a pipeline run this binary is never the go command, whatever argv
// says. Inside one, the go name and the go and tool words each reach it.
func TestLinkedGoArgs(t *testing.T) {
	t.Setenv(linkedGoEnv, "")
	_, linked := LinkedGoArgs([]string{"go", "build"})
	assert.False(t, linked)

	t.Setenv(linkedGoEnv, "1")
	args, linked := LinkedGoArgs([]string{"/tmp/link/go", "build", "."})
	assert.True(t, linked)
	assert.Equal(t, []string{"/tmp/link/go", "build", "."}, args)

	args, linked = LinkedGoArgs([]string{"go-toolchain", "go", "build", "."})
	assert.True(t, linked)
	assert.Equal(t, []string{"go", "build", "."}, args)

	args, linked = LinkedGoArgs([]string{"go-toolchain", "tool", "compile", "-V=full"})
	assert.True(t, linked)
	assert.Equal(t, []string{"go-toolchain", "tool", "compile", "-V=full"}, args)

	_, linked = LinkedGoArgs([]string{"go-toolchain", "matrix"})
	assert.False(t, linked)
}
