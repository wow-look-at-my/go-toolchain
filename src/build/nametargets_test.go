package build

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testModule = "github.com/wow-look-at-my/agentic-loop/go"

// A single main one level below the module root is named after the module,
// which is what makes `src/` build to the repository's own name.
func TestNameTargetsUsesTheModuleNameForALoneMain(t *testing.T) {
	targets, err := nameTargets([]string{testModule + "/src"}, testModule)
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, "go", targets[0].OutputName)
}

// Two mains one level down both derive the MODULE's name. Keeping whichever
// was seen first drops the other binary from a build that still reports
// success, so a contested name goes to nobody: each falls back to its own
// directory.
func TestNameTargetsFallsBackToTheDirectoryOnACollision(t *testing.T) {
	targets, err := nameTargets([]string{testModule + "/cli", testModule + "/todo_driver"}, testModule)
	require.NoError(t, err)

	names := make([]string, len(targets))
	for i, tgt := range targets {
		names[i] = tgt.OutputName
	}
	assert.ElementsMatch(t, []string{"cli", "todo_driver"}, names,
		"both mains must be built, and neither may answer to the module's name")
}

// The deeper packages already name themselves, so nothing moves.
func TestNameTargetsLeavesDeeperPackagesAlone(t *testing.T) {
	targets, err := nameTargets([]string{testModule + "/cmd/cai", testModule + "/cmd/todo_driver"}, testModule)
	require.NoError(t, err)

	names := make([]string, len(targets))
	for i, tgt := range targets {
		names[i] = tgt.OutputName
	}
	assert.ElementsMatch(t, []string{"cai", "todo_driver"}, names)
}

// A collision the directory fallback cannot break is not reachable from one
// module's packages -- but if it ever is, the build says so instead of
// dropping a binary.
func TestNameTargetsRefusesAnUnbreakableCollision(t *testing.T) {
	_, err := nameTargets([]string{testModule + "/a/cai", testModule + "/b/cai"}, testModule)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a/cai")
	assert.Contains(t, err.Error(), "b/cai")
	assert.Contains(t, err.Error(), `"cai"`)
}
