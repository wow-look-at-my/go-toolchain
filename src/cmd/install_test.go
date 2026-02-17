package cmd

import (
	"os"
	"path/filepath"
	
	"testing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

)

func TestCopyFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create source file
	srcPath := filepath.Join(tmpDir, "source")
	content := []byte("test content")
	require.NoError(t, os.WriteFile(srcPath, content, 0755))

	// Copy it
	dstPath := filepath.Join(tmpDir, "dest")
	require.NoError(t, copyFile(srcPath, dstPath))

	// Verify content
	got, err := os.ReadFile(dstPath)
	require.Nil(t, err)

	assert.Equal(t, string(content), string(got))

	// Verify permissions preserved
	srcInfo, _ := os.Stat(srcPath)
	dstInfo, _ := os.Stat(dstPath)
	assert.Equal(t, dstInfo.Mode(), srcInfo.Mode())
}

func TestCopyFileMissingSource(t *testing.T) {
	tmpDir := t.TempDir()
	err := copyFile("/nonexistent/file", filepath.Join(tmpDir, "dest"))
	assert.NotNil(t, err)
}

func TestCopyFileInvalidDest(t *testing.T) {
	tmpDir := t.TempDir()

	srcPath := filepath.Join(tmpDir, "source")
	require.NoError(t, os.WriteFile(srcPath, []byte("test"), 0644))

	err := copyFile(srcPath, "/nonexistent/dir/dest")
	assert.NotNil(t, err)
}

func TestCopyFileLargeFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a larger file to ensure io.Copy path is exercised
	srcPath := filepath.Join(tmpDir, "large")
	content := make([]byte, 10000)
	for i := range content {
		content[i] = byte(i % 256)
	}
	require.NoError(t, os.WriteFile(srcPath, content, 0755))

	dstPath := filepath.Join(tmpDir, "large_copy")
	require.NoError(t, copyFile(srcPath, dstPath))

	got, err := os.ReadFile(dstPath)
	require.Nil(t, err)

	assert.Equal(t, len(content), len(got))
}

func TestRunInstallImplSymlink(t *testing.T) {
	// runInstallImpl uses os.Executable() which returns the test binary
	// We can test that it creates a symlink to the correct target
	tmpDir := t.TempDir()
	targetDir := filepath.Join(tmpDir, "bin")

	// Override HOME so it installs to our temp dir
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	// Ensure .local/bin will be created inside tmpDir
	installCopy = false
	defer func() { installCopy = false }()

	err := runInstallImpl()
	require.Nil(t, err)

	// Check that something was created in ~/.local/bin
	expectedDir := filepath.Join(tmpDir, ".local", "bin")
	entries, err := os.ReadDir(expectedDir)
	require.Nil(t, err)
	require.NotEqual(t, 0, len(entries))

	// Verify it's a symlink
	targetPath := filepath.Join(expectedDir, entries[0].Name())
	info, err := os.Lstat(targetPath)
	require.Nil(t, err)
	assert.NotEqual(t, 0, info.Mode()&os.ModeSymlink)

	_ = targetDir // suppress unused warning
}

func TestRunInstallImplCopy(t *testing.T) {
	tmpDir := t.TempDir()

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	installCopy = true
	defer func() { installCopy = false }()

	err := runInstallImpl()
	require.Nil(t, err)

	// Check that something was created in ~/.local/bin
	expectedDir := filepath.Join(tmpDir, ".local", "bin")
	entries, err := os.ReadDir(expectedDir)
	require.Nil(t, err)
	require.NotEqual(t, 0, len(entries))

	// Verify it's NOT a symlink
	targetPath := filepath.Join(expectedDir, entries[0].Name())
	info, err := os.Lstat(targetPath)
	require.Nil(t, err)
	assert.Equal(t, os.FileMode(0), info.Mode()&os.ModeSymlink)
}

func TestRunInstallImplReplacesExisting(t *testing.T) {
	tmpDir := t.TempDir()

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	installCopy = false
	defer func() { installCopy = false }()

	// Run install twice — second should replace the first
	require.NoError(t, runInstallImpl())
	require.NoError(t, runInstallImpl())

	// Verify entries: binary + go-safe-build compat symlink
	expectedDir := filepath.Join(tmpDir, ".local", "bin")
	entries, err := os.ReadDir(expectedDir)
	require.Nil(t, err)
	assert.Equal(t, 2, len(entries))
}

func TestFileHash(t *testing.T) {
	tmpDir := t.TempDir()

	// Create two identical files and one different
	content := []byte("hello world")
	f1 := filepath.Join(tmpDir, "a")
	f2 := filepath.Join(tmpDir, "b")
	f3 := filepath.Join(tmpDir, "c")
	os.WriteFile(f1, content, 0644)
	os.WriteFile(f2, content, 0644)
	os.WriteFile(f3, []byte("different"), 0644)

	h1, err := fileHash(f1)
	require.Nil(t, err)
	h2, err := fileHash(f2)
	require.Nil(t, err)
	h3, err := fileHash(f3)
	require.Nil(t, err)

	assert.Equal(t, h2, h1)
	assert.NotEqual(t, h3, h1)
}

func TestFileHashMissing(t *testing.T) {
	_, err := fileHash("/nonexistent/file")
	assert.NotNil(t, err)
}

func TestInstallStatusNotInstalled(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	status := installStatus()
	assert.Contains(t, status, "not installed")
}

func TestInstallStatusSymlinkCurrent(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	// Install via symlink first
	installCopy = false
	defer func() { installCopy = false }()
	require.NoError(t, runInstallImpl())

	status := installStatus()
	assert.Contains(t, status, "current")
}

func TestInstallStatusSymlinkElsewhere(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	// Resolve current exe name to create a symlink pointing elsewhere
	exe, _ := os.Executable()
	currentPath, _ := filepath.EvalSymlinks(exe)
	binName := filepath.Base(currentPath)

	binDir := filepath.Join(tmpDir, ".local", "bin")
	os.MkdirAll(binDir, 0755)
	os.Symlink("/some/other/path", filepath.Join(binDir, binName))

	status := installStatus()
	assert.Contains(t, status, "points elsewhere")
}

func TestInstallStatusCopyCurrent(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	// Install via copy
	installCopy = true
	defer func() { installCopy = false }()
	require.NoError(t, runInstallImpl())

	status := installStatus()
	assert.Contains(t, status, "up to date")
}

func TestInstallStatusCopyOutdated(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	// Resolve current exe name
	exe, _ := os.Executable()
	currentPath, _ := filepath.EvalSymlinks(exe)
	binName := filepath.Base(currentPath)

	// Write a different file at the install path
	binDir := filepath.Join(tmpDir, ".local", "bin")
	os.MkdirAll(binDir, 0755)
	os.WriteFile(filepath.Join(binDir, binName), []byte("old binary"), 0755)

	status := installStatus()
	assert.Contains(t, status, "OUTDATED")
}
