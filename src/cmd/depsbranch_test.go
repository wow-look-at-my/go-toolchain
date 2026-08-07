package cmd

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"golang.org/x/mod/modfile"
)

func TestTrackedBranch(t *testing.T) {
	gomod := `module test
go 1.21

require (
	github.com/wow-look-at-my/foo v0.0.0-20240101120000-abc123def456 // go-toolchain:branch=v1
	github.com/wow-look-at-my/bar v0.0.0-20240101120000-abc123def456 // indirect
	github.com/spf13/cobra v1.8.0
)
`
	f, err := modfile.Parse("go.mod", []byte(gomod), nil)
	require.NoError(t, err)

	got := map[string]string{}
	for _, req := range f.Require {
		got[req.Mod.Path] = trackedBranch(req)
	}

	assert.Equal(t, "v1", got["github.com/wow-look-at-my/foo"])
	assert.Equal(t, "", got["github.com/wow-look-at-my/bar"])
	assert.Equal(t, "", got["github.com/spf13/cobra"])
}

func TestUpdateTrackedBranchDeps_NoGoMod(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := runner.NewMock()
	changed, err := UpdateTrackedBranchDeps(mock)
	assert.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, 0, len(mock.Calls()))
}

func TestUpdateTrackedBranchDeps_ParseError(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("go.mod", []byte("not valid go.mod content {{{"), 0644)

	mock := runner.NewMock()
	changed, err := UpdateTrackedBranchDeps(mock)
	assert.NoError(t, err)
	assert.False(t, changed)
}

func TestUpdateTrackedBranchDeps_NoMarkers(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	gomod := `module test
go 1.21

require github.com/spf13/cobra v1.8.0
`
	os.WriteFile("go.mod", []byte(gomod), 0644)

	mock := runner.NewMock()
	changed, err := UpdateTrackedBranchDeps(mock)
	assert.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, 0, len(mock.Calls()))
}

func TestUpdateTrackedBranchDeps_UpdatesVersion(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	gomod := `module test
go 1.21

require github.com/wow-look-at-my/foo v0.0.0-20200101000000-000000000000 // go-toolchain:branch=v1
`
	os.WriteFile("go.mod", []byte(gomod), 0644)

	fullHash := "abc123def456789012345678901234567890abcd"
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("git", "ls-remote") {
			require.Equal(t, "https://github.com/wow-look-at-my/foo", cfg.Args[1])
			require.Equal(t, "refs/heads/v1", cfg.Args[2])
			return runner.MockProcess([]byte(fullHash+"\trefs/heads/v1\n"), nil), nil
		}
		for _, arg := range cfg.Args {
			if arg == "init" || arg == "fetch" {
				return runner.MockProcess(nil, nil), nil
			}
			if arg == "log" {
				return runner.MockProcess([]byte("1700000000\n"), nil), nil
			}
		}
		return nil, nil
	}

	changed, err := UpdateTrackedBranchDeps(mock)
	require.NoError(t, err)
	assert.True(t, changed)

	data, err := os.ReadFile("go.mod")
	require.NoError(t, err)
	f, err := modfile.Parse("go.mod", data, nil)
	require.NoError(t, err)
	require.Len(t, f.Require, 1)
	assert.Contains(t, f.Require[0].Mod.Version, fullHash[:12])
	// The branch marker survives the rewrite.
	assert.Equal(t, "v1", trackedBranch(f.Require[0]))
}

func TestUpdateTrackedBranchDeps_NoChangeWhenSame(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// The pseudo-version already matches what ls-remote will report.
	fullHash := "abc123def456789012345678901234567890abcd"
	gomod := `module test
go 1.21

require github.com/wow-look-at-my/foo v0.0.0-20231114221320-abc123def456 // go-toolchain:branch=v1
`
	os.WriteFile("go.mod", []byte(gomod), 0644)

	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("git", "ls-remote") {
			return runner.MockProcess([]byte(fullHash+"\trefs/heads/v1\n"), nil), nil
		}
		for _, arg := range cfg.Args {
			if arg == "init" || arg == "fetch" {
				return runner.MockProcess(nil, nil), nil
			}
			if arg == "log" {
				return runner.MockProcess([]byte("1700000000\n"), nil), nil
			}
		}
		return nil, nil
	}

	changed, err := UpdateTrackedBranchDeps(mock)
	require.NoError(t, err)
	assert.False(t, changed)
}

func TestUpdateTrackedBranchDeps_IndirectSkipped(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	gomod := `module test
go 1.21

require github.com/wow-look-at-my/foo v0.0.0-20200101000000-000000000000 // indirect; go-toolchain:branch=v1
`
	os.WriteFile("go.mod", []byte(gomod), 0644)

	mock := runner.NewMock()
	changed, err := UpdateTrackedBranchDeps(mock)
	assert.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, 0, len(mock.Calls()))
}

func TestUpdateTrackedBranchDeps_GitFails(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	gomod := `module test
go 1.21

require github.com/wow-look-at-my/foo v0.0.0-20200101000000-000000000000 // go-toolchain:branch=v1
`
	os.WriteFile("go.mod", []byte(gomod), 0644)

	mock := runner.NewMock()
	mock.SetResponse("git", []string{"ls-remote", "https://github.com/wow-look-at-my/foo", "refs/heads/v1"}, nil, os.ErrNotExist)

	_, err := UpdateTrackedBranchDeps(mock)
	assert.Error(t, err)
}

func TestUpdateTrackedBranchDeps_NoRefFound(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	gomod := `module test
go 1.21

require github.com/wow-look-at-my/foo v0.0.0-20200101000000-000000000000 // go-toolchain:branch=nonexistent
`
	os.WriteFile("go.mod", []byte(gomod), 0644)

	mock := runner.NewMock()
	mock.SetResponse("git", []string{"ls-remote", "https://github.com/wow-look-at-my/foo", "refs/heads/nonexistent"}, []byte(""), nil)

	_, err := UpdateTrackedBranchDeps(mock)
	assert.Error(t, err)
}
