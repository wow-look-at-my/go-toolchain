package cmd

import (
	"errors"
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

func TestReexecUnderOwnBuildLeavesAConsumerAlone(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("go.mod", []byte("module example.com/consumer\n\ngo 1.27\n"), 0o644))
	assert.NoError(t, reexecUnderOwnBuild())
}

// A tidy failure stops the run before the self-build reads a short go.mod.
func TestReexecUnderOwnBuildTidiesFirst(t *testing.T) {
	t.Setenv(selfReexecEnv, "")
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("go.mod", []byte("module "+ownModulePath+"\n\ngo 1.27\n"), 0o644))
	require.NoError(t, os.WriteFile("main.go", []byte("package main\n\nfunc main() {}\n"), 0o644))

	tidied := 0
	orig := selfBuildTidy
	selfBuildTidy = func() error { tidied++; return errors.New("go mod tidy failed: sentinel") }
	t.Cleanup(func() { selfBuildTidy = orig })

	err := reexecUnderOwnBuild()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sentinel", "the tidy error must stop the run before the self-build")
	assert.Equal(t, 1, tidied)
}

func TestReexecUnderOwnBuildRunsOnceOnTheChild(t *testing.T) {
	t.Setenv(selfReexecEnv, "1")
	assert.NoError(t, reexecUnderOwnBuild())
}
