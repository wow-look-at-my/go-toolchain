package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestReexecUnderOwnBuildLeavesAConsumerAlone(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("go.mod", []byte("module example.com/consumer\n\ngo 1.27\n"), 0o644))
	assert.NoError(t, reexecUnderOwnBuild())
}

func TestReexecUnderOwnBuildRunsOnceOnTheChild(t *testing.T) {
	t.Setenv(selfReexecEnv, "1")
	assert.NoError(t, reexecUnderOwnBuild())
}
