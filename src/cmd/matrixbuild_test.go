package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
		outputPath: "/tmp/test",
	}

	err := runBuild(mock, job, nil)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "exit status 1")
	assert.Contains(t, err.Error(), "undefined: foo")
}

func TestRunBuildNoStderrOnSuccess(t *testing.T) {
	mock := runner.NewMock()
	job := buildJob{
		goos:       "linux",
		goarch:     "amd64",
		srcPath:    ".",
		outputPath: "/tmp/test",
	}

	err := runBuild(mock, job, nil)
	assert.Nil(t, err)
}

func TestRunBuild(t *testing.T) {
	oldCgo := cgoEnabled
	cgoEnabled = false
	defer func() { cgoEnabled = oldCgo }()

	mock := runner.NewMock()
	job := buildJob{
		goos:       "linux",
		goarch:     "amd64",
		srcPath:    ".",
		outputPath: "/tmp/test",
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

	// Verify -o flag
	hasOutput := false
	for i, arg := range cfg.Args {
		if arg == "-o" && i+1 < len(cfg.Args) {
			hasOutput = strings.Contains(cfg.Args[i+1], "/tmp/test")
		}
	}
	assert.True(t, hasOutput)
}

func TestRunBuildWithCgoEnabled(t *testing.T) {
	oldCgo := cgoEnabled
	cgoEnabled = true
	defer func() { cgoEnabled = oldCgo }()

	mock := runner.NewMock()
	job := buildJob{
		goos:       "linux",
		goarch:     "amd64",
		srcPath:    ".",
		outputPath: "/tmp/test",
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

func TestCreateHostBinaries(t *testing.T) {
	tmpDir := t.TempDir()

	targets := []build.Target{
		{ImportPath: "./cmd/mytool", OutputName: "mytool"},
	}

	// Create a fake host binary
	hostBinary := fmt.Sprintf("mytool_%s_%s", runtime.GOOS, runtime.GOARCH)
	os.WriteFile(filepath.Join(tmpDir, hostBinary), []byte("binary"), 0755)

	err := createHostBinaries(targets, tmpDir)
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

func TestCreateHostBinariesSkipMissing(t *testing.T) {
	tmpDir := t.TempDir()

	targets := []build.Target{
		{ImportPath: "./cmd/mytool", OutputName: "mytool"},
	}

	// Don't create the host binary — should skip without error
	err := createHostBinaries(targets, tmpDir)
	assert.Nil(t, err)

	// Symlinks should not exist
	_, err = os.Readlink(filepath.Join(tmpDir, "mytool_host"))
	assert.NotNil(t, err)
	_, err = os.Readlink(filepath.Join(tmpDir, "mytool"))
	assert.NotNil(t, err)
}

func TestCreateHostBinariesReplacesStale(t *testing.T) {
	tmpDir := t.TempDir()

	targets := []build.Target{
		{ImportPath: "./cmd/mytool", OutputName: "mytool"},
	}

	hostBinary := fmt.Sprintf("mytool_%s_%s", runtime.GOOS, runtime.GOARCH)
	os.WriteFile(filepath.Join(tmpDir, hostBinary), []byte("binary"), 0755)

	// Create stale symlinks pointing elsewhere
	os.Symlink("old_target", filepath.Join(tmpDir, "mytool_host"))
	os.Symlink("old_target", filepath.Join(tmpDir, "mytool"))

	err := createHostBinaries(targets, tmpDir)
	assert.Nil(t, err)

	linkTarget, _ := os.Readlink(filepath.Join(tmpDir, "mytool_host"))
	assert.Equal(t, hostBinary, linkTarget)
	linkTarget, _ = os.Readlink(filepath.Join(tmpDir, "mytool"))
	assert.Equal(t, hostBinary, linkTarget)
}

// The fat APE is the artifact that gets published. Running it rewrites its own
// file, so the convenience entry must be an assimilated copy of it, never a
// symlink to it.
func TestCreateHostBinariesCopiesAndAssimilatesTheAPE(t *testing.T) {
	hostMachineByte(t)
	tmpDir := t.TempDir()
	targets := []build.Target{
		{ImportPath: "./cmd/mytool", OutputName: "mytool"},
	}
	ape := filepath.Join(tmpDir, "mytool_cosmo_fat")
	original := []byte(apePrologue + strings.Repeat("\x00", 4096))
	require.NoError(t, os.WriteFile(ape, original, 0o755))

	require.NoError(t, createHostBinaries(targets, tmpDir))

	host := filepath.Join(tmpDir, "mytool_host")
	info, err := os.Lstat(host)
	require.NoError(t, err)
	assert.Zero(t, info.Mode()&os.ModeSymlink, "the APE must be copied, not linked")
	copied, err := os.ReadFile(host)
	require.NoError(t, err)
	assert.Equal(t, []byte("\x7fELF"), copied[:4], "the copy execs without rewriting itself")

	untouched, err := os.ReadFile(ape)
	require.NoError(t, err)
	assert.Equal(t, original, untouched, "the published artifact keeps its checksum")

	linkTarget, err := os.Readlink(filepath.Join(tmpDir, "mytool"))
	require.NoError(t, err)
	assert.Equal(t, "mytool_host", linkTarget)
}
