package cmd

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"golang.org/x/mod/modfile"
)

// defaultBranchMock answers `git ls-remote --symref <url> HEAD` with branch as
// the symbolic HEAD, plus the plumbing a pseudo-version derivation needs, and
// records the ls-remote argv so a test can assert which repository was asked.
func defaultBranchMock(t *testing.T, branch, fullHash string, epoch int64) (*runner.Mock, *[]string) {
	t.Helper()
	var lsRemote []string
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("git", "ls-remote") {
			lsRemote = append(lsRemote, strings.Join(cfg.Args[1:], " "))
			for _, arg := range cfg.Args {
				if arg == "--symref" {
					return runner.MockProcess([]byte("ref: refs/heads/"+branch+"\tHEAD\n"+fullHash+"\tHEAD\n"), nil), nil
				}
			}
			return runner.MockProcess([]byte(fullHash+"\tHEAD\n"), nil), nil
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

func writeGoMod(t *testing.T, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile("go.mod", []byte(content), 0644))
}

// suffixFor returns the rendered trailing comment of the go.mod line naming
// mod, from either the require or the replace block.
func suffixFor(t *testing.T, mod string) string {
	t.Helper()
	for _, line := range strings.Split(string(readGoMod(t)), "\n") {
		if !strings.Contains(line, mod) {
			continue
		}
		if idx := strings.Index(line, "//"); idx != -1 {
			return strings.TrimSpace(line[idx:])
		}
		return ""
	}
	t.Fatalf("no go.mod line mentions %s", mod)
	return ""
}

func TestEnforceOrgBranchTrackingMarksAVersionPinnedOrgRequire(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGoMod(t, `module test
go 1.21

require (
	github.com/spf13/cobra v1.8.0
	github.com/wow-look-at-my/foo v1.2.3
)
`)

	mock, lsRemote := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)

	changed, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "// go-toolchain:auto-branch", suffixFor(t, "wow-look-at-my/foo"))
	assert.Equal(t, "", suffixFor(t, "spf13/cobra"), "a third-party dependency is left alone")
	assert.Empty(t, *lsRemote, "the bare marker names no branch, so there is nothing to look up")
}

// The marker is only half the fix: the version pin it replaces is still the
// stale snapshot until the branch is resolved, which is the next step of the
// same pipeline phase.
func TestEnforceOrgBranchTrackingThenUpdateResolvesTheBranchHead(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGoMod(t, `module test
go 1.21

require github.com/wow-look-at-my/foo v1.2.3
`)

	mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)

	_, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	changed, err := UpdateTrackedBranchDeps(mock)
	require.NoError(t, err)
	assert.True(t, changed)

	f, err := modfile.Parse("go.mod", readGoMod(t), nil)
	require.NoError(t, err)
	require.Len(t, f.Require, 1)
	assert.Equal(t, "v0.0.0-20260812203640-351d2159f8d8", f.Require[0].Mod.Version)
	assert.Equal(t, marker{tracks: true}, parseMarker(f.Require[0].Syntax))
}

func TestEnforceOrgBranchTrackingLeavesAnAlreadyTrackedRequireAlone(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGoMod(t, `module test
go 1.21

require github.com/wow-look-at-my/foo v0.0.0-20240101120000-abc123def456 // go-toolchain:auto-branch=v1
`)

	mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)

	changed, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, "// go-toolchain:auto-branch=v1", suffixFor(t, "wow-look-at-my/foo"), "the chosen branch is not replaced by the default one")
	assert.Empty(t, mock.Calls())
}

// A marker on an indirect require tracks nothing, so the line cannot be
// rewritten -- but the module is version-pinned exactly like the ones that
// are, and a silent skip would read as compliance. It warns, and the warning
// names both repairs.
func TestEnforceOrgBranchTrackingWarnsOnAnIndirectOrgRequire(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGoMod(t, `module test
go 1.21

require github.com/wow-look-at-my/foo v1.2.3 // indirect
`)

	logger.ResetWarnCount()
	mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)

	changed, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	assert.False(t, changed, "the line is not rewritten -- the marker would not work there")
	assert.Empty(t, mock.Calls(), "and nothing is resolved for it")

	warnings := logger.EmittedWarnings()
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "github.com/wow-look-at-my/foo")
	assert.Contains(t, warnings[0], "replace")
	assert.Contains(t, warnings[0], pinnedMarker)
}

// The two ways out of that warning, plus the case where the module is not the
// org's problem: each silences it, because each is a real answer.
func TestEnforceOrgBranchTrackingIndirectWarningRespectsItsOwnEscapes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		gomod string
	}{
		{"a tracked replace pins the effective version", `module test
go 1.21

require github.com/wow-look-at-my/foo v1.2.3 // indirect

replace github.com/wow-look-at-my/foo => github.com/wow-look-at-my/foo v0.0.0-20200101000000-000000000000 // go-toolchain:branch=master
`},
		{"a deliberate pin says so", `module test
go 1.21

require github.com/wow-look-at-my/foo v1.2.3 // indirect; go-toolchain:pinned upstream break
`},
		{"a third-party module is not covered at all", `module test
go 1.21

require github.com/spf13/cobra v1.8.0 // indirect
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			writeGoMod(t, tc.gomod)

			logger.ResetWarnCount()
			mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)

			_, err := EnforceOrgBranchTracking(mock)
			require.NoError(t, err)
			assert.Empty(t, logger.EmittedWarnings())
		})
	}
}

// A fork keeps upstream's module path, so the version that reaches the build
// lives on the replace line -- which is where the marker has to go.
func TestEnforceOrgBranchTrackingMarksTheReplaceNotTheRequire(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGoMod(t, `module test
go 1.21

require charm.land/bubbletea/v2 v2.0.8

replace charm.land/bubbletea/v2 => github.com/wow-look-at-my/bubbletea/v2 v2.0.0-20200101000000-000000000000
`)

	mock, lsRemote := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)

	changed, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "// go-toolchain:auto-branch", suffixFor(t, "wow-look-at-my/bubbletea/v2"))
	assert.Empty(t, *lsRemote, "the bare marker names no branch, so there is nothing to look up")
}

func TestEnforceOrgBranchTrackingSkipsALocalReplacement(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGoMod(t, `module test
go 1.21

require github.com/wow-look-at-my/foo v1.2.3

replace github.com/wow-look-at-my/foo => ./vendor/foo
`)

	mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)

	changed, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	assert.False(t, changed, "a directory has no branch to track, and the require's version is overridden by it")
	assert.Empty(t, untrackedOrgDeps())
}

func TestEnforceOrgBranchTrackingHonorsTheDeliberatePinOptOut(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGoMod(t, `module test
go 1.21

require github.com/wow-look-at-my/foo v1.2.3 // go-toolchain:pinned v2 is a hard API break
`)

	mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)

	changed, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Empty(t, untrackedOrgDeps())
	assert.Empty(t, mock.Calls())
}

// Migration is the one path here that still needs the remote: it has to know
// whether the hardcoded name is just the default branch written down. Leaving
// the legacy marker in place on a resolution failure would report a green run
// for a migration that did not happen.
func TestEnforceOrgBranchTrackingFailsWhenTheDefaultBranchCannotBeResolved(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGoMod(t, `module test
go 1.21

require github.com/wow-look-at-my/foo v1.2.3 // go-toolchain:branch=master
`)

	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("git", "ls-remote") {
			return runner.MockProcess(nil, assert.AnError), nil
		}
		return nil, nil
	}

	_, err := EnforceOrgBranchTracking(mock)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be migrated")
	assert.Contains(t, string(readGoMod(t)), legacyBranchMarker, "the unmigrated line stays exactly as it was")
}

func TestEnforceOrgBranchTrackingIsANoOpWithoutAGoMod(t *testing.T) {
	t.Chdir(t.TempDir())

	mock := runner.NewMock()
	changed, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Empty(t, mock.Calls())
}

func TestUntrackedOrgDepsNamesEveryUnmarkedLine(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGoMod(t, `module test
go 1.21

require (
	github.com/spf13/cobra v1.8.0
	github.com/wow-look-at-my/bar v0.0.0-20240101120000-abc123def456 // go-toolchain:branch=v1
	github.com/wow-look-at-my/foo v1.2.3
)

replace charm.land/bubbletea/v2 => github.com/wow-look-at-my/bubbletea/v2 v2.0.0-20200101000000-000000000000
`)

	assert.ElementsMatch(t,
		[]string{"github.com/wow-look-at-my/foo", "github.com/wow-look-at-my/bubbletea/v2"},
		untrackedOrgDeps())
}

func TestIsOrgModule(t *testing.T) {
	assert.True(t, isOrgModule("github.com/wow-look-at-my/foo"))
	assert.True(t, isOrgModule("github.com/wow-look-at-my/foo/v2"))
	assert.False(t, isOrgModule("github.com/spf13/cobra"))
	assert.False(t, isOrgModule("github.com/wow-look-at-my-else/foo"))
}
