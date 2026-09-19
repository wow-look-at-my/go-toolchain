package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// at is a dependency directive naming command.
func toolDirective(command string) generateDirective {
	return generateDirective{File: "/gomodcache/x/text@v1/catalog.go", Line: 7, Command: command, Label: "x/text/catalog.go"}
}

// The go command runs its own directives, so only a separate program is named.
func TestDirectiveToolNamesTheProgramToInstall(t *testing.T) {
	t.Serial()
	assert.Equal(t, "stringer", directiveTool(toolDirective("stringer -type=Kind")))
	assert.Equal(t, "gotext", directiveTool(toolDirective("gotext -out catalog.gen.go update")))
	assert.Empty(t, directiveTool(toolDirective("go run gen.go -out catalog.gen.go")))
	assert.Empty(t, directiveTool(toolDirective("")))
}

// Every generator a dependency names carries a pinned version, so a run that
// meets a new name says which one to add rather than skipping the directive.
func TestAnUnpinnedGeneratorIsReportedRatherThanSkipped(t *testing.T) {
	t.Serial()
	mock := runner.NewMock()
	err := installGenerators(mock, []generateDirective{toolDirective("not-a-real-generator -out catalog.gen.go")})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not-a-real-generator")
	assert.Contains(t, err.Error(), "generatorPackages", "the message names where the pin belongs")
	assert.Empty(t, mock.Calls(), "nothing is installed until the pin exists")
}

// A tool already on PATH needs no install, so a directive naming it starts none.
func TestAnInstalledGeneratorIsNotBuiltAgain(t *testing.T) {
	t.Serial()
	mock := runner.NewMock()
	require.NoError(t, installGenerators(mock, []generateDirective{toolDirective("sh -out catalog.gen.go")}))
	assert.Empty(t, mock.Calls(), "sh is on PATH already")

	require.NoError(t, installGenerators(mock, nil))
	assert.Empty(t, mock.Calls())
}

// The pinned set covers the generators this repository's own dependencies name.
func TestThePinnedGeneratorsCarryAVersionEach(t *testing.T) {
	t.Serial()
	for tool, pkg := range generatorPackages {
		assert.Contains(t, pkg, "@", "%s is pinned to a version", tool)
		assert.Contains(t, pkg, tool, "%s names the program it builds", tool)
	}
}
