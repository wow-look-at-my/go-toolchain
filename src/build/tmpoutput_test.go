package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTempOutputPath(t *testing.T) {
	for _, tc := range []struct{ final, want string }{
		{filepath.Join("build", "mytool"), filepath.Join("build", ".tmp-mytool")},
		{filepath.Join("build", "mytool.exe"), filepath.Join("build", ".tmp-mytool.exe")},
		{filepath.Join("build", "mytool_linux_amd64"), filepath.Join("build", ".tmp-mytool_linux_amd64")},
		{filepath.Join("build", "mytool_wasm_js"), filepath.Join("build", ".tmp-mytool_wasm_js")},
		{"mytool", ".tmp-mytool"},
	} {
		assert.Equal(t, tc.want, TempOutputPath(tc.final), "temp path for %s", tc.final)
	}
}

func TestCommitOutputMovesTempOntoTarget(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "mytool")
	require.NoError(t, os.WriteFile(TempOutputPath(final), []byte("FAT-APE"), 0o755))

	require.NoError(t, CommitOutput(final))

	body, err := os.ReadFile(final)
	require.NoError(t, err)
	assert.Equal(t, "FAT-APE", string(body))
	assert.NoFileExists(t, TempOutputPath(final), "the temp spelling must not survive the commit")
}

func TestCommitOutputReplacesTheTargetFile(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "mytool")
	require.NoError(t, os.WriteFile(final, []byte("STALE"), 0o755))
	require.NoError(t, os.WriteFile(TempOutputPath(final), []byte("FRESH"), 0o755))

	require.NoError(t, CommitOutput(final))

	body, err := os.ReadFile(final)
	require.NoError(t, err)
	assert.Equal(t, "FRESH", string(body), "the move replaces whatever sat at the target name")
}

func TestCommitOutputMovesSidecarsAndSticksToItsOwnShapes(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "mytool")
	require.NoError(t, os.WriteFile(TempOutputPath(final), []byte("APE"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, TmpPrefix+"mytool.elf"), []byte("SIDE"), 0o644))
	// Another build's temp output: a separate -o target with its own commit.
	require.NoError(t, os.WriteFile(filepath.Join(dir, TmpPrefix+"mytool_linux_amd64"), []byte("NATIVE"), 0o755))

	require.NoError(t, CommitOutput(final))

	assert.FileExists(t, filepath.Join(dir, "mytool"))
	body, err := os.ReadFile(filepath.Join(dir, "mytool.elf"))
	require.NoError(t, err, "sidecar ELFs take their name from the -o path and move with it")
	assert.Equal(t, "SIDE", string(body))
	body, err = os.ReadFile(filepath.Join(dir, TmpPrefix+"mytool_linux_amd64"))
	require.NoError(t, err, "a <base>_… temp belongs to its own target and must not move here")
	assert.Equal(t, "NATIVE", string(body))
}

func TestCommitOutputFailsWhenTheBuildWroteNothing(t *testing.T) {
	final := filepath.Join(t.TempDir(), "mytool")

	err := CommitOutput(final)
	assert.ErrorContains(t, err, "wrote no output")
	assert.NoFileExists(t, final, "nothing may materialize at the target name")
}

func TestDiscardOutputRemovesTheTempSpellingsOnly(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "mytool")
	require.NoError(t, os.WriteFile(TempOutputPath(final), []byte("PARTIAL"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, TmpPrefix+"mytool.elf"), []byte("SIDE"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, TmpPrefix+"other_linux_amd64"), []byte("OTHER"), 0o755))
	require.NoError(t, os.WriteFile(final, []byte("KEEP"), 0o755))

	DiscardOutput(final)

	assert.NoFileExists(t, TempOutputPath(final))
	assert.NoFileExists(t, filepath.Join(dir, TmpPrefix+"mytool.elf"))
	body, err := os.ReadFile(filepath.Join(dir, TmpPrefix+"other_linux_amd64"))
	require.NoError(t, err, "another target's temp output is not this build's to discard")
	assert.Equal(t, "OTHER", string(body))
	body, err = os.ReadFile(final)
	require.NoError(t, err, "DiscardOutput never touches the target file")
	assert.Equal(t, "KEEP", string(body))
}
