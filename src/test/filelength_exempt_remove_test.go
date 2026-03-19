package test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestRemoveFileLengthExemption(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0644))

	// Exempt it first
	require.NoError(t, ExemptFileLength(path))

	// Verify exempt
	ok, err := IsFileLengthExempt(path)
	require.NoError(t, err)
	require.True(t, ok)

	// Remove exemption
	require.NoError(t, RemoveFileLengthExemption(path))

	// Verify no longer exempt
	ok, err = IsFileLengthExempt(path)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestRemoveFileLengthExemptionWhenNoneExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0644))

	// Should not error when removing a non-existent exemption
	require.NoError(t, RemoveFileLengthExemption(path))
}
