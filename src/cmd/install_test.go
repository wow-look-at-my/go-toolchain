package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create source file
	srcPath := filepath.Join(tmpDir, "source")
	content := []byte("test content")
	if err := os.WriteFile(srcPath, content, 0755); err != nil {
		t.Fatalf("failed to create source file: %v", err)
	}

	// Copy it
	dstPath := filepath.Join(tmpDir, "dest")
	if err := copyFile(srcPath, dstPath); err != nil {
		t.Fatalf("copyFile failed: %v", err)
	}

	// Verify content
	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("failed to read dest file: %v", err)
	}

	if string(got) != string(content) {
		t.Errorf("content mismatch: expected %q, got %q", content, got)
	}

	// Verify permissions preserved
	srcInfo, _ := os.Stat(srcPath)
	dstInfo, _ := os.Stat(dstPath)
	if srcInfo.Mode() != dstInfo.Mode() {
		t.Errorf("mode mismatch: expected %v, got %v", srcInfo.Mode(), dstInfo.Mode())
	}
}

func TestCopyFileMissingSource(t *testing.T) {
	tmpDir := t.TempDir()
	err := copyFile("/nonexistent/file", filepath.Join(tmpDir, "dest"))
	if err == nil {
		t.Error("expected error for missing source")
	}
}

func TestCopyFileInvalidDest(t *testing.T) {
	tmpDir := t.TempDir()

	srcPath := filepath.Join(tmpDir, "source")
	if err := os.WriteFile(srcPath, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create source file: %v", err)
	}

	err := copyFile(srcPath, "/nonexistent/dir/dest")
	if err == nil {
		t.Error("expected error for invalid dest path")
	}
}

func TestCopyFileLargeFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a larger file to ensure io.Copy path is exercised
	srcPath := filepath.Join(tmpDir, "large")
	content := make([]byte, 10000)
	for i := range content {
		content[i] = byte(i % 256)
	}
	if err := os.WriteFile(srcPath, content, 0755); err != nil {
		t.Fatalf("failed to create source file: %v", err)
	}

	dstPath := filepath.Join(tmpDir, "large_copy")
	if err := copyFile(srcPath, dstPath); err != nil {
		t.Fatalf("copyFile failed: %v", err)
	}

	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("failed to read dest file: %v", err)
	}

	if len(got) != len(content) {
		t.Errorf("size mismatch: expected %d, got %d", len(content), len(got))
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that something was created in ~/.local/bin
	expectedDir := filepath.Join(tmpDir, ".local", "bin")
	entries, err := os.ReadDir(expectedDir)
	if err != nil {
		t.Fatalf("failed to read target dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one file in target dir")
	}

	// Verify it's a symlink
	targetPath := filepath.Join(expectedDir, entries[0].Name())
	info, err := os.Lstat(targetPath)
	if err != nil {
		t.Fatalf("failed to lstat target: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("expected symlink, got regular file")
	}

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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that something was created in ~/.local/bin
	expectedDir := filepath.Join(tmpDir, ".local", "bin")
	entries, err := os.ReadDir(expectedDir)
	if err != nil {
		t.Fatalf("failed to read target dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one file in target dir")
	}

	// Verify it's NOT a symlink
	targetPath := filepath.Join(expectedDir, entries[0].Name())
	info, err := os.Lstat(targetPath)
	if err != nil {
		t.Fatalf("failed to lstat target: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("expected regular file, got symlink")
	}
}

func TestRunInstallImplReplacesExisting(t *testing.T) {
	tmpDir := t.TempDir()

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	installCopy = false
	defer func() { installCopy = false }()

	// Run install twice — second should replace the first
	if err := runInstallImpl(); err != nil {
		t.Fatalf("first install failed: %v", err)
	}
	if err := runInstallImpl(); err != nil {
		t.Fatalf("second install failed: %v", err)
	}

	// Verify entries: binary + go-safe-build compat symlink
	expectedDir := filepath.Join(tmpDir, ".local", "bin")
	entries, err := os.ReadDir(expectedDir)
	if err != nil {
		t.Fatalf("failed to read target dir: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries (binary + compat symlink), got %d", len(entries))
	}
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
	if err != nil {
		t.Fatalf("fileHash(a): %v", err)
	}
	h2, err := fileHash(f2)
	if err != nil {
		t.Fatalf("fileHash(b): %v", err)
	}
	h3, err := fileHash(f3)
	if err != nil {
		t.Fatalf("fileHash(c): %v", err)
	}

	if h1 != h2 {
		t.Error("identical files should have same hash")
	}
	if h1 == h3 {
		t.Error("different files should have different hashes")
	}
}

func TestFileHashMissing(t *testing.T) {
	_, err := fileHash("/nonexistent/file")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestInstallStatusNotInstalled(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	status := installStatus()
	if !strings.Contains(status, "not installed") {
		t.Errorf("expected 'not installed', got: %s", status)
	}
}

func TestInstallStatusSymlinkCurrent(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	// Install via symlink first
	installCopy = false
	defer func() { installCopy = false }()
	if err := runInstallImpl(); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	status := installStatus()
	if !strings.Contains(status, "current") {
		t.Errorf("expected 'current' for symlink to self, got: %s", status)
	}
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
	if !strings.Contains(status, "points elsewhere") {
		t.Errorf("expected 'points elsewhere', got: %s", status)
	}
}

func TestInstallStatusCopyCurrent(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	// Install via copy
	installCopy = true
	defer func() { installCopy = false }()
	if err := runInstallImpl(); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	status := installStatus()
	if !strings.Contains(status, "up to date") {
		t.Errorf("expected 'up to date' for matching copy, got: %s", status)
	}
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
	if !strings.Contains(status, "OUTDATED") {
		t.Errorf("expected 'OUTDATED' for mismatched copy, got: %s", status)
	}
}
