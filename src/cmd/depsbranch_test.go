package cmd

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
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
		got[req.Mod.Path] = trackedBranch(req.Syntax)
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
	assert.Equal(t, "v1", trackedBranch(f.Require[0].Syntax))
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

// gitLsRemoteMock answers ls-remote with one commit and the plumbing the
// pseudo-version derivation needs, so a test can say "the branch is HERE now".
func gitLsRemoteMock(t *testing.T, fullHash string) *runner.Mock {
	t.Helper()
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
	return mock
}

// gitBranchMock answers ls-remote for ref on any repository, plus the
// plumbing the pseudo-version derivation needs, and records the ls-remote
// argv so a test can assert WHICH repository and ref were resolved.
func gitBranchMock(t *testing.T, fullHash, ref string, epoch int64) (*runner.Mock, *[]string) {
	t.Helper()
	var lsRemote []string
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("git", "ls-remote") {
			lsRemote = append(lsRemote, strings.Join(cfg.Args[1:], " "))
			return runner.MockProcess([]byte(fullHash+"\t"+ref+"\n"), nil), nil
		}
		for _, arg := range cfg.Args {
			if arg == "init" || arg == "fetch" {
				return runner.MockProcess(nil, nil), nil
			}
			if arg == "log" {
				return runner.MockProcess([]byte(strconv.FormatInt(epoch, 10)+"\n"), nil), nil
			}
		}
		return nil, nil
	}
	return mock, &lsRemote
}

// A fork keeps upstream's module path, so it is consumed through a replace
// and the require line names UPSTREAM. The marker on the replace tracks the
// replacement's repository, and rewrites the replacement's version.
func TestUpdateTrackedBranchDepsFollowsTheMarkerOnAReplaceLine(t *testing.T) {
	t.Chdir(t.TempDir())
	gomod := `module test
go 1.21

require charm.land/bubbletea/v2 v2.0.8

replace charm.land/bubbletea/v2 => github.com/wow-look-at-my/bubbletea/v2 v2.0.0-20200101000000-000000000000 // go-toolchain:branch=master
`
	require.NoError(t, os.WriteFile("go.mod", []byte(gomod), 0644))

	const fullHash = "351d2159f8d8a85613aa2a6e98c8c63df3c98623"
	mock, lsRemote := gitBranchMock(t, fullHash, "refs/heads/master", 1786567000)

	changed, err := UpdateTrackedBranchDeps(mock)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, []string{"https://github.com/wow-look-at-my/bubbletea/v2 refs/heads/master"}, *lsRemote)

	f, err := modfile.Parse("go.mod", readGoMod(t), nil)
	require.NoError(t, err)
	require.Len(t, f.Replace, 1)
	// The measured shape of go list -m github.com/wow-look-at-my/bubbletea/v2@master.
	assert.Equal(t, "v2.0.0-20260812203640-351d2159f8d8", f.Replace[0].New.Version)
	assert.Equal(t, "github.com/wow-look-at-my/bubbletea/v2", f.Replace[0].New.Path)
	assert.Equal(t, "master", trackedBranch(f.Replace[0].Syntax))
	// The require names upstream; tracking its branch is never what the
	// marker means, so it must not move.
	require.Len(t, f.Require, 1)
	assert.Equal(t, "v2.0.8", f.Require[0].Mod.Version)
}

// A replacement into a local directory has no remote and no version. The
// marker on one is a mistake, and a mistake gets said out loud.
func TestUpdateTrackedBranchDepsSkipsAFilesystemReplacement(t *testing.T) {
	logger.ResetWarnCount()
	defer logger.ResetWarnCount()

	t.Chdir(t.TempDir())
	gomod := `module test
go 1.21

require example.com/foo v1.2.3

replace example.com/foo => ../foo // go-toolchain:branch=master
`
	require.NoError(t, os.WriteFile("go.mod", []byte(gomod), 0644))

	mock := runner.NewMock()
	mock.Handler = func(runner.Config) (runner.IProcess, error) {
		t.Fatal("a local directory has no branch to resolve")
		return nil, nil
	}

	changed, err := UpdateTrackedBranchDeps(mock)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, gomod, string(readGoMod(t)))
	require.Len(t, logger.EmittedWarnings(), 1)
	assert.Contains(t, logger.EmittedWarnings()[0].Message, "../foo")
}

// An absolute replacement target is the same case wearing a different path.
func TestIsLocalReplacement(t *testing.T) {
	local := []module.Version{
		{Path: "../foo"},
		{Path: "./foo"},
		{Path: "/opt/foo"},
		{Path: "example.com/foo"}, // no version: a directory named like a module
	}
	for _, target := range local {
		assert.True(t, isLocalReplacement(target), target.Path)
	}
	assert.False(t, isLocalReplacement(module.Version{Path: "example.com/foo", Version: "v1.2.3"}))
}

func TestUpdateTrackedBranchDepsLeavesAnUnmarkedReplaceAlone(t *testing.T) {
	t.Chdir(t.TempDir())
	gomod := `module test
go 1.21

require example.com/foo v1.2.3

replace example.com/foo => github.com/wow-look-at-my/foo v0.0.0-20200101000000-000000000000
`
	require.NoError(t, os.WriteFile("go.mod", []byte(gomod), 0644))

	mock := runner.NewMock()
	mock.Handler = func(runner.Config) (runner.IProcess, error) {
		t.Fatal("an unmarked replace must cost no ref resolution")
		return nil, nil
	}

	changed, err := UpdateTrackedBranchDeps(mock)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, gomod, string(readGoMod(t)))
	assert.False(t, trackedBranchDepsMoved(mock))
}

// The up-to-date fast exit has to see a moved branch behind a replace too.
func TestTrackedBranchDepsMovedSeesAMovedReplacement(t *testing.T) {
	t.Chdir(t.TempDir())
	gomod := `module test
go 1.21

require example.com/foo v1.2.3

replace example.com/foo => github.com/wow-look-at-my/foo v0.0.0-20200101000000-000000000000 // go-toolchain:branch=master
`
	require.NoError(t, os.WriteFile("go.mod", []byte(gomod), 0644))

	mock, _ := gitBranchMock(t, "abc123def456789012345678901234567890abcd", "refs/heads/master", 1700000000)
	assert.True(t, trackedBranchDepsMoved(mock))
}

func readGoMod(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("go.mod")
	require.NoError(t, err)
	return data
}

func writeTrackedGoMod(t *testing.T, version string) {
	t.Helper()
	t.Chdir(t.TempDir())
	gomod := "module test\ngo 1.21\n\nrequire github.com/wow-look-at-my/foo " + version +
		" // go-toolchain:branch=v1\n"
	require.NoError(t, os.WriteFile("go.mod", []byte(gomod), 0644))
}

// The up-to-date fast exit hashes files, and a tracked branch's HEAD is not
// one. Without this check a moved dependency was invisible: the tree was
// unchanged, so the run exited before the updater that exists to notice.
func TestTrackedBranchDepsMovedSeesAMovedBranchOnAnUnchangedTree(t *testing.T) {
	writeTrackedGoMod(t, "v0.0.0-20200101000000-000000000000")
	moved := gitLsRemoteMock(t, "abc123def456789012345678901234567890abcd")
	assert.True(t, trackedBranchDepsMoved(moved))
}

func TestTrackedBranchDepsMovedIsFalseWhenTheBranchIsWhereGoModSaysItIs(t *testing.T) {
	const hash = "abc123def456789012345678901234567890abcd"
	writeTrackedGoMod(t, "v0.0.0-20231114221320-"+hash[:12])
	assert.False(t, trackedBranchDepsMoved(gitLsRemoteMock(t, hash)))
}

// An unreachable remote must not read as "everything changed": that would turn
// a network blip into a full rebuild, which is the opposite of what a cache
// check is for. The real run reports the failure.
func TestTrackedBranchDepsMovedIsFalseWhenItCannotTell(t *testing.T) {
	writeTrackedGoMod(t, "v0.0.0-20200101000000-000000000000")
	mock := runner.NewMock()
	mock.Handler = func(runner.Config) (runner.IProcess, error) {
		return runner.MockProcess(nil, errors.New("no network")), nil
	}
	assert.False(t, trackedBranchDepsMoved(mock))

	t.Chdir(t.TempDir()) // no go.mod at all
	assert.False(t, trackedBranchDepsMoved(mock))
}

// A repository with no tracked require must pay nothing for this check.
func TestTrackedBranchDepsMovedMakesNoCallWithoutATrackedRequire(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("go.mod",
		[]byte("module test\ngo 1.21\n\nrequire github.com/wow-look-at-my/foo v1.2.3\n"), 0644))
	mock := runner.NewMock()
	mock.Handler = func(runner.Config) (runner.IProcess, error) {
		t.Fatal("an untracked require must cost no ref resolution")
		return nil, nil
	}
	assert.False(t, trackedBranchDepsMoved(mock))
}
