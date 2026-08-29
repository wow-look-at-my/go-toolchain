package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/build"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

func TestRunBuildCapturesStderr(t *testing.T) {
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "build") {
			return runner.MockProcessWithStderr(nil, []byte("./main.go:5:3: undefined: foo\n"), fmt.Errorf("exit status 1")), nil
		}
		return nil, nil
	}

	job := buildJob{
		goos:       "linux",
		goarch:     "amd64",
		srcPath:    ".",
		outputPath: filepath.Join(t.TempDir(), "out"),
	}

	err := runBuild(mock, job, nil)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "exit status 1")
	assert.Contains(t, err.Error(), "undefined: foo")
}

func TestRunBuildNoStderrOnSuccess(t *testing.T) {
	mock := runner.NewMock()
	// The mocked compiler obeys the real one's contract: an exit-0 go build
	// materializes its -o target.
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		writeMockBuildOutput(cfg, "bin")
		return runner.MockProcess(nil, nil), nil
	}
	job := buildJob{
		goos:       "linux",
		goarch:     "amd64",
		srcPath:    ".",
		outputPath: filepath.Join(t.TempDir(), "out"),
	}

	err := runBuild(mock, job, nil)
	assert.Nil(t, err)
	assert.FileExists(t, job.outputPath, "the commit moved the build onto the target name")
}

func TestRunBuild(t *testing.T) {
	oldCgo := cgoEnabled
	cgoEnabled = false
	defer func() { cgoEnabled = oldCgo }()

	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		writeMockBuildOutput(cfg, "bin")
		return runner.MockProcess(nil, nil), nil
	}
	job := buildJob{
		goos:       "linux",
		goarch:     "amd64",
		srcPath:    ".",
		outputPath: filepath.Join(t.TempDir(), "out"),
	}

	err := runBuild(mock, job, nil)
	assert.Nil(t, err)

	// Verify command was called
	calls := mock.Calls()
	assert.Equal(t, 1, len(calls))

	// Verify env vars were set
	cfg := calls[0]
	goos, _ := cfg.Env.Get("GOOS")
	goarch, _ := cfg.Env.Get("GOARCH")
	cgo, _ := cfg.Env.Get("CGO_ENABLED")
	assert.Equal(t, "linux", goos)
	assert.Equal(t, "amd64", goarch)
	assert.Equal(t, "0", cgo)

	// -o is the .tmp- spelling, never the target file itself.
	hasOutput := false
	for i, arg := range cfg.Args {
		if arg == "-o" && i+1 < len(cfg.Args) {
			hasOutput = cfg.Args[i+1] == build.TempOutputPath(job.outputPath)
		}
	}
	assert.True(t, hasOutput)
}

func TestRunBuildWithCgoEnabled(t *testing.T) {
	oldCgo := cgoEnabled
	cgoEnabled = true
	defer func() { cgoEnabled = oldCgo }()

	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		writeMockBuildOutput(cfg, "bin")
		return runner.MockProcess(nil, nil), nil
	}
	job := buildJob{
		goos:       "linux",
		goarch:     "amd64",
		srcPath:    ".",
		outputPath: filepath.Join(t.TempDir(), "out"),
	}

	err := runBuild(mock, job, nil)
	assert.Nil(t, err)

	calls := mock.Calls()
	assert.Equal(t, 1, len(calls))

	cfg := calls[0]
	goos2, _ := cfg.Env.Get("GOOS")
	goarch2, _ := cfg.Env.Get("GOARCH")
	assert.Equal(t, "linux", goos2)
	assert.Equal(t, "amd64", goarch2)
	hasCgo := cfg.Env.Contains("CGO_ENABLED")
	assert.False(t, hasCgo, "CGO_ENABLED should not be set when --cgo is used")
}

func TestCreateHostSymlinks(t *testing.T) {
	tmpDir := t.TempDir()

	targets := []build.Target{
		{ImportPath: "./cmd/mytool", OutputName: "mytool"},
	}

	// Create a fake host binary
	hostBinary := fmt.Sprintf("mytool_%s_%s", runtime.GOOS, runtime.GOARCH)
	os.WriteFile(filepath.Join(tmpDir, hostBinary), []byte("binary"), 0755)

	err := createHostSymlinks(targets, tmpDir)
	assert.Nil(t, err)

	// Check _host symlink
	linkTarget, err := os.Readlink(filepath.Join(tmpDir, "mytool_host"))
	assert.Nil(t, err)
	assert.Equal(t, hostBinary, linkTarget)

	// Check bare symlink
	linkTarget, err = os.Readlink(filepath.Join(tmpDir, "mytool"))
	assert.Nil(t, err)
	assert.Equal(t, hostBinary, linkTarget)
}

func TestCreateHostSymlinksSkipsMissing(t *testing.T) {
	tmpDir := t.TempDir()

	targets := []build.Target{
		{ImportPath: "./cmd/mytool", OutputName: "mytool"},
	}

	// Don't create the host binary — should skip without error
	err := createHostSymlinks(targets, tmpDir)
	assert.Nil(t, err)

	// Symlinks should not exist
	_, err = os.Readlink(filepath.Join(tmpDir, "mytool_host"))
	assert.NotNil(t, err)
	_, err = os.Readlink(filepath.Join(tmpDir, "mytool"))
	assert.NotNil(t, err)
}

func TestCreateHostSymlinksReplacesStale(t *testing.T) {
	tmpDir := t.TempDir()

	targets := []build.Target{
		{ImportPath: "./cmd/mytool", OutputName: "mytool"},
	}

	hostBinary := fmt.Sprintf("mytool_%s_%s", runtime.GOOS, runtime.GOARCH)
	os.WriteFile(filepath.Join(tmpDir, hostBinary), []byte("binary"), 0755)

	// Create stale symlinks pointing elsewhere
	os.Symlink("old_target", filepath.Join(tmpDir, "mytool_host"))
	os.Symlink("old_target", filepath.Join(tmpDir, "mytool"))

	err := createHostSymlinks(targets, tmpDir)
	assert.Nil(t, err)

	linkTarget, _ := os.Readlink(filepath.Join(tmpDir, "mytool_host"))
	assert.Equal(t, hostBinary, linkTarget)
	linkTarget, _ = os.Readlink(filepath.Join(tmpDir, "mytool"))
	assert.Equal(t, hostBinary, linkTarget)
}

// TestRunBuildMovesOutputIntoPlace pins the write-then-move contract from
// outside runBuild: the -o arg carries the .tmp- spelling, the result ends up
// on the target file, and the temp name is gone.
func TestRunBuildMovesOutputIntoPlace(t *testing.T) {
	final := filepath.Join(t.TempDir(), "mytool")
	var built string
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		for i, arg := range cfg.Args {
			if arg == "-o" && i+1 < len(cfg.Args) {
				built = cfg.Args[i+1]
			}
		}
		require.Equal(t, build.TempOutputPath(final), built, "-o must carry the temp spelling")
		require.NoError(t, os.WriteFile(built, []byte("BIN"), 0o755))
		return runner.MockProcess(nil, nil), nil
	}

	require.NoError(t, runBuild(mock, buildJob{srcPath: ".", outputPath: final}, nil))

	body, err := os.ReadFile(final)
	require.NoError(t, err)
	assert.Equal(t, "BIN", string(body), "the target file holds what the build wrote")
	assert.NoFileExists(t, build.TempOutputPath(final), "the temp spelling must not survive the commit")
}

// TestRunBuildDeletesTempOutputOnFailure: a failed build leaves nothing —
// what the compiler already wrote under the temp spelling is removed, and the
// target file never appears.
func TestRunBuildDeletesTempOutputOnFailure(t *testing.T) {
	final := filepath.Join(t.TempDir(), "mytool")
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "build") {
			writeMockBuildOutput(cfg, "PARTIAL")
			return runner.MockProcessWithStderr(nil, []byte("./main.go:5:3: undefined: foo\n"), fmt.Errorf("exit status 1")), nil
		}
		return nil, nil
	}

	err := runBuild(mock, buildJob{srcPath: ".", outputPath: final}, nil)
	require.NotNil(t, err)
	assert.Contains(t, err.Error(), "undefined: foo")
	assert.NoFileExists(t, build.TempOutputPath(final), "the failed build's temp output must be deleted")
	assert.NoFileExists(t, final, "a failed build must not produce the target file")
}

// TestRunBuildRefusesToCommitMissingOutput: go build exiting 0 without
// producing its -o target is not shippable — the run fails loudly instead of
// reporting a build whose output nobody can find.
func TestRunBuildRefusesToCommitMissingOutput(t *testing.T) {
	final := filepath.Join(t.TempDir(), "mytool")
	// A build that "succeeds" but writes nothing, unlike a real one.
	mock := runner.NewMock()

	assert.Error(t, runBuild(mock, buildJob{srcPath: ".", outputPath: final}, nil))
	assert.NoFileExists(t, final)
}
