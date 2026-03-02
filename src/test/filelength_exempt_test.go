package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wow-look-at-my/testify/require"
)

func TestExemptFileLength(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.go")
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("x\n", 600)), 0644))

	// Not exempt initially
	ok, err := IsFileLengthExempt(path)
	require.NoError(t, err)
	require.False(t, ok)

	// Exempt it
	require.NoError(t, ExemptFileLength(path))

	// Now exempt
	ok, err = IsFileLengthExempt(path)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestExemptFileLengthStaleAfterModify(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.go")
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("x\n", 600)), 0644))

	require.NoError(t, ExemptFileLength(path))

	// Modify file
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("y\n", 601)), 0644))

	// Exemption should be stale
	ok, err := IsFileLengthExempt(path)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestIsFileLengthExemptNonexistentFile(t *testing.T) {
	ok, err := IsFileLengthExempt("/tmp/does-not-exist-go-toolchain-test")
	require.Error(t, err)
	require.False(t, ok)
}

func TestIsFileLengthExemptNoAttr(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "small.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0644))

	ok, err := IsFileLengthExempt(path)
	require.NoError(t, err)
	require.False(t, ok)
}
