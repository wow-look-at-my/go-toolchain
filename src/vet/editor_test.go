package vet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyEditorRequireWritesOnDiffer(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	require.NoError(t, os.WriteFile(p, []byte("old"), 0o644))

	ed := NewEditor(true)
	wrote, err := ed.Require(p, []byte("new"), "reason")
	require.NoError(t, err)
	assert.True(t, wrote)

	got, _ := os.ReadFile(p)
	assert.Equal(t, "new", string(got))
	assert.NoError(t, ed.Err())
}

func TestApplyEditorNoopWhenEqual(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	require.NoError(t, os.WriteFile(p, []byte("same"), 0o644))

	ed := NewEditor(true)
	wrote, err := ed.Require(p, []byte("same"), "reason")
	require.NoError(t, err)
	assert.False(t, wrote)
}

func TestApplyEditorApplyWrites(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	require.NoError(t, os.WriteFile(p, []byte("old"), 0o644))

	ed := NewEditor(true)
	wrote, err := ed.Apply(p, []byte("new"))
	require.NoError(t, err)
	assert.True(t, wrote)

	got, _ := os.ReadFile(p)
	assert.Equal(t, "new", string(got))
}

func TestCheckEditorRequireRecordsAndNeverWrites(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	require.NoError(t, os.WriteFile(p, []byte("old"), 0o644))

	ed := NewEditor(false)
	wrote, err := ed.Require(p, []byte("new"), "needs fixing")
	require.NoError(t, err)
	assert.False(t, wrote)

	// File untouched on CI.
	got, _ := os.ReadFile(p)
	assert.Equal(t, "old", string(got))

	// Violation recorded with path + reason, plus a unified diff readable without running code.
	require.Error(t, ed.Err())
	msg := ed.Err().Error()
	assert.Contains(t, msg, p)
	assert.Contains(t, msg, "needs fixing")
	assert.Contains(t, msg, "-old")
	assert.Contains(t, msg, "+new")
}

func TestCheckEditorCleanNoViolation(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	require.NoError(t, os.WriteFile(p, []byte("same"), 0o644))

	ed := NewEditor(false)
	wrote, err := ed.Require(p, []byte("same"), "reason")
	require.NoError(t, err)
	assert.False(t, wrote)
	assert.NoError(t, ed.Err())
}

func TestCheckEditorApplyIsNoop(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	require.NoError(t, os.WriteFile(p, []byte("old"), 0o644))

	ed := NewEditor(false)
	// Apply changes are dropped on CI (reported via diagnostic instead); nothing is written or recorded.
	wrote, err := ed.Apply(p, []byte("new"))
	require.NoError(t, err)
	assert.False(t, wrote)

	got, _ := os.ReadFile(p)
	assert.Equal(t, "old", string(got))
	assert.NoError(t, ed.Err())
}
