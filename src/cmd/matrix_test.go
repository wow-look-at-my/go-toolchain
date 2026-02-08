package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunReleaseWithRunnerNoPlatforms(t *testing.T) {
	oldOS := matrixOS
	oldArch := matrixArch
	matrixOS = []string{}
	matrixArch = []string{}
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
	}()

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

	oldOS := matrixOS
	oldArch := matrixArch
	oldOutput := outputDir
	oldSrcPath := srcPath
	matrixOS = []string{"linux"}
	matrixArch = []string{"amd64"}
	outputDir = filepath.Join(tmpDir, "dist")
	srcPath = "."
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
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

	oldOS := matrixOS
	oldArch := matrixArch
	oldOutput := outputDir
	oldSrcPath := srcPath
	oldParallel := releaseParallel
	matrixOS = []string{"linux"}
	matrixArch = []string{"amd64", "arm64"}
	outputDir = filepath.Join(tmpDir, "dist")
	srcPath = "."
	releaseParallel = 2
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
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

	oldOS := matrixOS
	oldArch := matrixArch
	oldOutput := outputDir
	oldSrcPath := srcPath
	oldParallel := releaseParallel
	matrixOS = []string{"linux"}
	matrixArch = []string{"amd64"}
	outputDir = filepath.Join(tmpDir, "dist")
	srcPath = "."
	releaseParallel = 1
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
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

	oldOS := matrixOS
	oldArch := matrixArch
	oldOutput := outputDir
	oldSrcPath := srcPath
	oldParallel := releaseParallel
	matrixOS = []string{"windows"}
	matrixArch = []string{"amd64"}
	outputDir = filepath.Join(tmpDir, "dist")
	srcPath = "."
	releaseParallel = 1
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
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

	oldOS := matrixOS
	oldArch := matrixArch
	oldOutput := outputDir
	oldSrcPath := srcPath
	oldParallel := releaseParallel
	matrixOS = []string{"linux"}
	matrixArch = []string{"amd64"}
	outputDir = filepath.Join(tmpDir, "dist")
	srcPath = "."
	releaseParallel = 10 // More workers than jobs
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
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

func TestRunReleaseWithRunnerMultipleOSArch(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Create a minimal go file
	os.WriteFile("main.go", []byte("package main\nfunc main() {}\n"), 0644)

	oldOS := matrixOS
	oldArch := matrixArch
	oldOutput := outputDir
	oldSrcPath := srcPath
	oldParallel := releaseParallel
	matrixOS = []string{"linux", "darwin"}
	matrixArch = []string{"amd64", "arm64"}
	outputDir = filepath.Join(tmpDir, "dist")
	srcPath = "."
	releaseParallel = 4
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
		outputDir = oldOutput
		srcPath = oldSrcPath
		releaseParallel = oldParallel
	}()

	mock := NewMockRunner()
	err := runReleaseWithRunner(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Should have 4 builds: 2 OS x 2 arch
	buildCount := 0
	for _, cmd := range mock.Commands {
		if cmd.Name == "go" && len(cmd.Args) > 0 && cmd.Args[0] == "build" {
			buildCount++
		}
	}
	if buildCount != 4 {
		t.Errorf("expected 4 builds (2 OS x 2 arch), got %d", buildCount)
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
