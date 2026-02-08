package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParsePlatforms(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"linux/amd64", []string{"linux/amd64"}},
		{"linux/amd64,darwin/arm64", []string{"linux/amd64", "darwin/arm64"}},
		{"linux/amd64, darwin/arm64", []string{"linux/amd64", "darwin/arm64"}},
		{"", []string{}},
		{"invalid", []string{}},
		{"linux/amd64,,darwin/arm64", []string{"linux/amd64", "darwin/arm64"}},
	}

	for _, tt := range tests {
		result := parsePlatforms(tt.input)
		if len(result) != len(tt.expected) {
			t.Errorf("parsePlatforms(%q): got %v, want %v", tt.input, result, tt.expected)
			continue
		}
		for i, v := range result {
			if v != tt.expected[i] {
				t.Errorf("parsePlatforms(%q)[%d]: got %q, want %q", tt.input, i, v, tt.expected[i])
			}
		}
	}
}

func TestRunReleaseWithRunnerNoPlatforms(t *testing.T) {
	oldPlatforms := releasePlatforms
	releasePlatforms = ""
	defer func() { releasePlatforms = oldPlatforms }()

	mock := NewMockRunner()
	err := runReleaseWithRunner(mock)
	if err == nil {
		t.Error("expected error with no platforms")
	}
}

func TestRunReleaseWithRunnerNoMainPackages(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	oldPlatforms := releasePlatforms
	oldOutput := outputDir
	oldSrcPath := srcPath
	releasePlatforms = "linux/amd64"
	outputDir = filepath.Join(tmpDir, "dist")
	srcPath = "."
	defer func() {
		releasePlatforms = oldPlatforms
		outputDir = oldOutput
		srcPath = oldSrcPath
	}()

	mock := NewMockRunner()
	err := runReleaseWithRunner(mock)
	if err == nil {
		t.Error("expected error with no main packages")
	}
}

func TestRunReleaseWithRunnerSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Create a minimal go file
	os.WriteFile("main.go", []byte("package main\nfunc main() {}\n"), 0644)

	oldPlatforms := releasePlatforms
	oldOutput := outputDir
	oldSrcPath := srcPath
	oldParallel := releaseParallel
	releasePlatforms = "linux/amd64,linux/arm64"
	outputDir = filepath.Join(tmpDir, "dist")
	srcPath = "."
	releaseParallel = 2
	defer func() {
		releasePlatforms = oldPlatforms
		outputDir = oldOutput
		srcPath = oldSrcPath
		releaseParallel = oldParallel
	}()

	mock := NewMockRunner()
	err := runReleaseWithRunner(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunReleaseWithRunnerBuildFails(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Create a minimal go file
	os.WriteFile("main.go", []byte("package main\nfunc main() {}\n"), 0644)

	oldPlatforms := releasePlatforms
	oldOutput := outputDir
	oldSrcPath := srcPath
	oldParallel := releaseParallel
	releasePlatforms = "linux/amd64"
	outputDir = filepath.Join(tmpDir, "dist")
	srcPath = "."
	releaseParallel = 1
	defer func() {
		releasePlatforms = oldPlatforms
		outputDir = oldOutput
		srcPath = oldSrcPath
		releaseParallel = oldParallel
	}()

	// Use a mock that fails all RunWithEnv calls
	mock := &buildEnvFailMockRunner{}
	err := runReleaseWithRunner(mock)
	if err == nil {
		t.Error("expected error when build fails")
	}
}

// buildEnvFailMockRunner fails all RunWithEnv calls
type buildEnvFailMockRunner struct {
	MockCommandRunner
}

func (m *buildEnvFailMockRunner) RunWithEnv(env []string, name string, args ...string) error {
	return os.ErrPermission
}

func TestRunReleaseWithRunnerWindowsExt(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Create a minimal go file
	os.WriteFile("main.go", []byte("package main\nfunc main() {}\n"), 0644)

	oldPlatforms := releasePlatforms
	oldOutput := outputDir
	oldSrcPath := srcPath
	oldParallel := releaseParallel
	releasePlatforms = "windows/amd64"
	outputDir = filepath.Join(tmpDir, "dist")
	srcPath = "."
	releaseParallel = 1
	defer func() {
		releasePlatforms = oldPlatforms
		outputDir = oldOutput
		srcPath = oldSrcPath
		releaseParallel = oldParallel
	}()

	mock := NewMockRunner()
	err := runReleaseWithRunner(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Check that commands were recorded with .exe extension
	found := false
	for _, cmd := range mock.Commands {
		if cmd.Name == "go" && len(cmd.Args) >= 3 && cmd.Args[0] == "build" {
			if filepath.Ext(cmd.Args[2]) == ".exe" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected .exe extension for windows build")
	}
}

func TestRunReleaseWithRunnerMoreJobsThanWorkers(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Create a minimal go file
	os.WriteFile("main.go", []byte("package main\nfunc main() {}\n"), 0644)

	oldPlatforms := releasePlatforms
	oldOutput := outputDir
	oldSrcPath := srcPath
	oldParallel := releaseParallel
	releasePlatforms = "linux/amd64"
	outputDir = filepath.Join(tmpDir, "dist")
	srcPath = "."
	releaseParallel = 10 // More workers than jobs
	defer func() {
		releasePlatforms = oldPlatforms
		outputDir = oldOutput
		srcPath = oldSrcPath
		releaseParallel = oldParallel
	}()

	mock := NewMockRunner()
	err := runReleaseWithRunner(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunBuild(t *testing.T) {
	mock := NewMockRunner()
	job := buildJob{
		goos:       "linux",
		goarch:     "amd64",
		srcPath:    ".",
		outputPath: "/tmp/test",
	}

	err := runBuild(mock, job)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify command was called
	if len(mock.Commands) != 1 {
		t.Errorf("expected 1 command, got %d", len(mock.Commands))
	}
}
