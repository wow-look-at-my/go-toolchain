package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

func TestSelfIsFixedPointComparesBytes(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	exe := filepath.Join(dir, "self")
	require.NoError(t, os.WriteFile(exe, []byte("toolchain"), 0o755))
	same := filepath.Join(dir, "same")
	require.NoError(t, os.WriteFile(same, []byte("toolchain"), 0o755))
	other := filepath.Join(dir, "other")
	require.NoError(t, os.WriteFile(other, []byte("toolchain2"), 0o755))
	orig := selfExecutableFunc
	selfExecutableFunc = func() (string, error) { return exe, nil }
	defer func() { selfExecutableFunc = orig }()

	got, err := selfIsFixedPoint(same)
	require.NoError(t, err)
	assert.True(t, got, "the same bytes are the fixed point")

	got, err = selfIsFixedPoint(other)
	require.NoError(t, err)
	assert.False(t, got, "different bytes are not")

	_, err = selfIsFixedPoint(filepath.Join(dir, "missing"))
	assert.Error(t, err)
}

func TestBuildSelfCommitsTheFixedPointThisRunProved(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	proved := filepath.Join(dir, "proved")
	require.NoError(t, os.WriteFile(proved, []byte("fixed point"), 0o755))
	fixedPointSelf, fixedPointDir = proved, ""
	defer func() { fixedPointSelf = "" }()

	out := filepath.Join(dir, "build", "go-toolchain")
	require.NoError(t, os.MkdirAll(filepath.Dir(out), 0o755))
	require.NoError(t, buildSelf(nil, buildJob{outputPath: out}, nil))

	got, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, "fixed point", string(got))
}

// A cold pass runs for minutes without output. The stall warning must name the phase of the pass, not only the outer step.
func TestBuildSelfPassNamesItsPhaseToTheWatchdog(t *testing.T) {
	t.Serial()
	watchdog := &outputWatchdog{}
	orig := activeWatchdog
	activeWatchdog = watchdog
	defer func() { activeWatchdog = orig }()

	var during string
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		during, _ = watchdog.stepName.Load().(string)
		return runner.MockProcess(nil, errors.New("embedstd failed")), nil
	}
	job := buildJob{outputPath: filepath.Join(t.TempDir(), "go-toolchain"), goroot: t.TempDir()}
	_, err := buildSelfPass(mock, job, []string{"go"}, t.TempDir(), 2, nil)
	require.Error(t, err)
	assert.Equal(t, "pass 2: standard library", during, "the watchdog names the phase that is running")
	stepAfter, _ := watchdog.stepName.Load().(string)
	assert.Empty(t, stepAfter, "a failed phase clears its name")
}

func TestReexecUnderOwnBuildLeavesAConsumerAlone(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("go.mod", []byte("module example.com/consumer\n\ngo 1.27\n"), 0o644))
	assert.NoError(t, reexecUnderOwnBuild())
}

func TestReexecUnderOwnBuildRunsOnceOnTheChild(t *testing.T) {
	t.Setenv(selfReexecEnv, "1")
	assert.NoError(t, reexecUnderOwnBuild())
}
