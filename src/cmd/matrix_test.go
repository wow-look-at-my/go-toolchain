package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"github.com/stretchr/testify/assert"

)

// matrixTestRunner provides test output with 100% coverage for matrix tests
type matrixTestRunner struct {
	MockCommandRunner
}

func (m *matrixTestRunner) RunWithPipes(name string, args ...string) (io.Reader, func() error, error) {
	m.mu.Lock()
	m.Commands = append(m.Commands, MockCommand{Name: name, Args: args})
	m.mu.Unlock()
	writeMockCoverProfile(args, 100)
	output := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"coverage: 100% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg"}
`
	return bytes.NewReader([]byte(output)), func() error { return nil }, nil
}

func newMatrixTestRunner() *matrixTestRunner {
	return &matrixTestRunner{
		MockCommandRunner: MockCommandRunner{
			Responses: make(map[string]MockResponse),
		},
	}
}

func TestRunReleaseWithRunnerNoPlatforms(t *testing.T) {
	oldOS := matrixOS
	oldArch := matrixArch
	matrixOS = []string{}
	matrixArch = []string{}
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
	}()

	mock := newMatrixTestRunner()
	err := runReleaseWithRunner(mock)
	assert.NotNil(t, err)
}

func TestRunReleaseWithRunnerNoMainPackages(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	oldOS := matrixOS
	oldArch := matrixArch
	oldOutput := outputDir
	matrixOS = []string{"linux"}
	matrixArch = []string{"amd64"}
	outputDir = filepath.Join(tmpDir, "dist")
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
		outputDir = oldOutput
	}()

	mock := newMatrixTestRunner()
	err := runReleaseWithRunner(mock)
	assert.NotNil(t, err)
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
	oldParallel := releaseParallel
	matrixOS = []string{"linux"}
	matrixArch = []string{"amd64", "arm64"}
	outputDir = filepath.Join(tmpDir, "dist")
	releaseParallel = 2
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
		outputDir = oldOutput
		releaseParallel = oldParallel
	}()

	mock := newMatrixTestRunner()
	err := runReleaseWithRunner(mock)
	assert.Nil(t, err)
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
	oldParallel := releaseParallel
	matrixOS = []string{"linux"}
	matrixArch = []string{"amd64"}
	outputDir = filepath.Join(tmpDir, "dist")
	releaseParallel = 1
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
		outputDir = oldOutput
		releaseParallel = oldParallel
	}()

	// Use a mock that fails all RunWithEnv calls
	mock := &buildEnvFailMockRunner{}
	err := runReleaseWithRunner(mock)
	assert.NotNil(t, err)
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
	oldParallel := releaseParallel
	matrixOS = []string{"windows"}
	matrixArch = []string{"amd64"}
	outputDir = filepath.Join(tmpDir, "dist")
	releaseParallel = 1
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
		outputDir = oldOutput
		releaseParallel = oldParallel
	}()

	mock := newMatrixTestRunner()
	err := runReleaseWithRunner(mock)
	assert.Nil(t, err)

	// Check that commands were recorded with .exe extension
	found := false
	for _, cmd := range mock.Commands {
		if cmd.Name == "go" && len(cmd.Args) > 0 && cmd.Args[0] == "build" {
			for i, arg := range cmd.Args {
				if arg == "-o" && i+1 < len(cmd.Args) {
					if filepath.Ext(cmd.Args[i+1]) == ".exe" {
						found = true
					}
				}
			}
		}
	}
	assert.True(t, found)
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
	oldParallel := releaseParallel
	matrixOS = []string{"linux"}
	matrixArch = []string{"amd64"}
	outputDir = filepath.Join(tmpDir, "dist")
	releaseParallel = 10 // More workers than jobs
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
		outputDir = oldOutput
		releaseParallel = oldParallel
	}()

	mock := newMatrixTestRunner()
	err := runReleaseWithRunner(mock)
	assert.Nil(t, err)
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
	oldParallel := releaseParallel
	matrixOS = []string{"linux", "darwin"}
	matrixArch = []string{"amd64", "arm64"}
	outputDir = filepath.Join(tmpDir, "dist")
	releaseParallel = 4
	defer func() {
		matrixOS = oldOS
		matrixArch = oldArch
		outputDir = oldOutput
		releaseParallel = oldParallel
	}()

	mock := newMatrixTestRunner()
	err := runReleaseWithRunner(mock)
	assert.Nil(t, err)

	// Should have 4 builds: 2 OS x 2 arch
	buildCount := 0
	for _, cmd := range mock.Commands {
		if cmd.Name == "go" && len(cmd.Args) > 0 && cmd.Args[0] == "build" {
			buildCount++
		}
	}
	assert.Equal(t, 4, buildCount)
}

func TestRunBuild(t *testing.T) {
	mock := newMatrixTestRunner()
	job := buildJob{
		goos:       "linux",
		goarch:     "amd64",
		srcPath:    ".",
		outputPath: "/tmp/test",
	}

	err := runBuild(mock, job)
	assert.Nil(t, err)

	// Verify command was called
	assert.Equal(t, 1, len(mock.Commands))
}
