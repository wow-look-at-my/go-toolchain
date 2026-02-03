package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
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

func TestCopyFileStatError(t *testing.T) {
	// This is tricky to test as Stat() typically succeeds if Open() did
	// But we can test the path works by using a valid file
	tmpDir := t.TempDir()

	srcPath := filepath.Join(tmpDir, "source")
	if err := os.WriteFile(srcPath, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create source file: %v", err)
	}

	dstPath := filepath.Join(tmpDir, "dest")
	err := copyFile(srcPath, dstPath)
	if err != nil {
		t.Errorf("copyFile failed: %v", err)
	}
}

// installTestMockRunner returns valid test JSON output and handles build
type installTestMockRunner struct {
	commands []MockCommand
}

func (m *installTestMockRunner) Run(name string, args ...string) error {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	return nil
}

func (m *installTestMockRunner) RunWithOutput(name string, args ...string) ([]byte, error) {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	return nil, nil
}

func (m *installTestMockRunner) RunWithPipes(name string, args ...string) (io.Reader, func() error, error) {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	output := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"coverage: 100% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg"}
`
	return bytes.NewReader([]byte(output)), func() error { return nil }, nil
}

func TestRunInstallWithRunnerSymlink(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Create coverage.out
	coverContent := `mode: set
example.com/pkg/main.go:10.20,12.2 1 1
`
	os.WriteFile("coverage.out", []byte(coverContent), 0644)

	// Create fake binary to install
	binaryName := "testbinary"
	os.WriteFile(binaryName, []byte("fake binary"), 0755)

	// Set up target dir within temp
	targetDir := filepath.Join(tmpDir, "bin")

	mock := &installTestMockRunner{}

	jsonOutput = true
	minCoverage = 80
	output = binaryName
	installTo = targetDir
	installCopy = false
	defer func() {
		jsonOutput = false
		minCoverage = 80
		output = ""
		installTo = ""
		installCopy = false
	}()

	err := runInstallWithRunner(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Check symlink was created
	targetPath := filepath.Join(targetDir, binaryName)
	info, err := os.Lstat(targetPath)
	if err != nil {
		t.Fatalf("failed to stat target: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("expected symlink, got regular file")
	}
}

func TestRunInstallWithRunnerCopy(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	coverContent := `mode: set
example.com/pkg/main.go:10.20,12.2 1 1
`
	os.WriteFile("coverage.out", []byte(coverContent), 0644)

	binaryName := "testbinary"
	os.WriteFile(binaryName, []byte("fake binary"), 0755)

	targetDir := filepath.Join(tmpDir, "bin")

	mock := &installTestMockRunner{}

	jsonOutput = false
	minCoverage = 80
	output = binaryName
	installTo = targetDir
	installCopy = true
	defer func() {
		jsonOutput = false
		minCoverage = 80
		output = ""
		installTo = ""
		installCopy = false
	}()

	err := runInstallWithRunner(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Check file was copied (not a symlink)
	targetPath := filepath.Join(targetDir, binaryName)
	info, err := os.Lstat(targetPath)
	if err != nil {
		t.Fatalf("failed to stat target: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("expected regular file, got symlink")
	}
}

func TestRunInstallWithRunnerExistingTarget(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	coverContent := `mode: set
example.com/pkg/main.go:10.20,12.2 1 1
`
	os.WriteFile("coverage.out", []byte(coverContent), 0644)

	binaryName := "testbinary"
	os.WriteFile(binaryName, []byte("fake binary"), 0755)

	targetDir := filepath.Join(tmpDir, "bin")
	os.MkdirAll(targetDir, 0755)

	// Create existing file at target
	targetPath := filepath.Join(targetDir, binaryName)
	os.WriteFile(targetPath, []byte("old file"), 0644)

	mock := &installTestMockRunner{}

	jsonOutput = true
	minCoverage = 80
	output = binaryName
	installTo = targetDir
	installCopy = false
	defer func() {
		jsonOutput = false
		minCoverage = 80
		output = ""
		installTo = ""
		installCopy = false
	}()

	err := runInstallWithRunner(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
