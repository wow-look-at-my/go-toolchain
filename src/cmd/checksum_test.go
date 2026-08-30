package cmd

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateChecksums(t *testing.T) {
	tmpDir := t.TempDir()

	// Create dummy binaries with known content
	files := map[string]string{
		"tool_linux_amd64":       "linux-amd64-binary",
		"tool_darwin_arm64":      "darwin-arm64-binary",
		"tool_windows_amd64.exe": "windows-amd64-binary",
	}

	var paths []string
	for name, content := range files {
		p := filepath.Join(tmpDir, name)
		os.WriteFile(p, []byte(content), 0755)
		paths = append(paths, p)
	}

	outPath, err := generateChecksums(tmpDir, paths)
	assert.Nil(t, err)
	assert.Equal(t, filepath.Join(tmpDir, "checksums.txt"), outPath)

	data, err := os.ReadFile(outPath)
	assert.Nil(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Equal(t, 3, len(lines))

	// Verify sorted order
	var names []string
	for _, line := range lines {
		parts := strings.SplitN(line, "  ", 2)
		assert.Equal(t, 2, len(parts), "line should have hash and filename separated by two spaces: %s", line)
		assert.Equal(t, 64, len(parts[0]), "hash should be 64 hex chars")
		names = append(names, parts[1])
	}
	assert.Equal(t, names[0], "tool_darwin_arm64")
	assert.Equal(t, names[1], "tool_linux_amd64")
	assert.Equal(t, names[2], "tool_windows_amd64.exe")

	// Verify the actual hash for a single file
	expectedHash := fmt.Sprintf("%x", sha256.Sum256([]byte("darwin-arm64-binary")))
	assert.True(t, strings.HasPrefix(lines[0], expectedHash), "first line should start with hash of darwin-arm64 binary")
}

func TestGenerateChecksumsEmpty(t *testing.T) {
	tmpDir := t.TempDir()

	outPath, err := generateChecksums(tmpDir, nil)
	assert.Nil(t, err)
	assert.Equal(t, "", outPath)

	// checksums.txt should not exist
	_, err = os.Stat(filepath.Join(tmpDir, "checksums.txt"))
	assert.True(t, os.IsNotExist(err))
}
