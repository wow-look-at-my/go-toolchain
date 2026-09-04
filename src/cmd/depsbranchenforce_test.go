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
	t.Serial()
	t.Chdir(t.TempDir())
	writeGoMod(t, `module test
go 1.21

require (
	github.com/spf13/cobra v1.8.0
	github.com/wow-look-at-my/foo v1.2.3
)
`)

	mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)

	changed, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "// go-toolchain:auto-branch", suffixFor(t, "wow-look-at-my/foo"))
	assert.Equal(t, "", suffixFor(t, "spf13/cobra"), "a third-party dependency is left alone")
	assert.Empty(t, mock.Calls(), "the written marker names no branch, so there is nothing to ask")
}

// The marker is only half the fix: the version pin it replaces is still the
// stale snapshot until the branch is resolved, which is the next step of the
// same pipeline phase.
func TestEnforceOrgBranchTrackingThenUpdateResolvesTheBranchHead(t *testing.T) {
	t.Serial()
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

// The legacy spelling always names a branch, so migrating it asks the remote
// a single question: is that name merely the default branch? A name that repeats
// the default is dropped, and the line stops caring what the branch is called.
// A name that does not is kept, because it was a deliberate choice.
func TestEnforceOrgBranchTrackingMigratesTheLegacySpelling(t *testing.T) {
	t.Serial()
	for _, tc := range []struct {
		branch string
		want   string
	}{
		{"master", "// go-toolchain:auto-branch"},
		{"v1", "// go-toolchain:auto-branch=v1"},
	} {
		t.Run(tc.branch, func(t *testing.T) {
			t.Chdir(t.TempDir())
			writeGoMod(t, `module test
go 1.21

require github.com/wow-look-at-my/foo v0.0.0-20240101120000-abc123def456 // go-toolchain:branch=`+tc.branch+`
`)

			mock, lsRemote := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)

			changed, err := EnforceOrgBranchTracking(mock)
			require.NoError(t, err)
			assert.True(t, changed)
			assert.Equal(t, tc.want, suffixFor(t, "wow-look-at-my/foo"))
			assert.Contains(t, strings.Join(*lsRemote, "\n"), "--symref")
		})
	}
}

// An indirect org require gets the same bare marker a direct require does: the
// module is version-pinned exactly like a direct require, and it has no
// direct require of its own to ride along with.
func TestEnforceOrgBranchTrackingMarksAVersionPinnedIndirectOrgRequire(t *testing.T) {
	t.Serial()
	t.Chdir(t.TempDir())
	writeGoMod(t, `module test
go 1.21

require github.com/wow-look-at-my/foo v1.2.3 // indirect
`)

	mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)

	changed, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "// indirect; go-toolchain:auto-branch", suffixFor(t, "wow-look-at-my/foo"))
}

// An indirect require from outside the org is not this org's problem and is left alone.
func TestEnforceOrgBranchTrackingLeavesAThirdPartyIndirectRequireAlone(t *testing.T) {
	t.Serial()
	t.Chdir(t.TempDir())
	writeGoMod(t, `module test
go 1.21

require github.com/spf13/cobra v1.8.0 // indirect
`)

	logger.ResetWarnCount()
	mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)

	changed, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Empty(t, logger.EmittedWarnings())
}

// A fork keeps upstream's module path, so the version that reaches the build
// lives on the replace line -- which is where the marker has to go.
func TestEnforceOrgBranchTrackingMarksTheReplaceNotTheRequire(t *testing.T) {
	t.Serial()
	t.Chdir(t.TempDir())
	writeGoMod(t, `module test
go 1.21

require charm.land/bubbletea/v2 v2.0.8

replace charm.land/bubbletea/v2 => github.com/wow-look-at-my/bubbletea/v2 v2.0.0-20200101000000-000000000000
`)

	mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)

	changed, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "// go-toolchain:auto-branch", suffixFor(t, "wow-look-at-my/bubbletea/v2"))
}

// A local replace is main-module-only: it points this repository at a
// directory and tells a consumer nothing. The consumer resolves the REQUIRE's
// version, so that line still has to track. The replace itself stays bare,
// because a directory has no branch.
func TestEnforceOrgBranchTrackingMarksARequireBehindALocalReplacement(t *testing.T) {
	t.Serial()
	t.Chdir(t.TempDir())
	writeGoMod(t, `module test
go 1.21

require github.com/wow-look-at-my/foo v1.2.3

replace github.com/wow-look-at-my/foo => ./vendor/foo
`)

	mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)

	changed, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "// go-toolchain:auto-branch", suffixFor(t, "wow-look-at-my/foo v1.2.3"))
	assert.Equal(t, "", suffixFor(t, "=> ./vendor/foo"), "a directory has no branch to track")
	assert.Empty(t, untrackedOrgDeps())
}

// The shape that made this a real outage: a multi-module repository requires
// its own sibling and hides the pin with a relative replace. Every consumer
// resolves the require, so the pin has to keep moving. Leaving it alone let it
// name a commit older than the sibling's own go.mod, and every consumer got
// "missing go.mod at revision".
func TestEnforceOrgBranchTrackingMarksASiblingRequireBehindARelativeReplace(t *testing.T) {
	t.Serial()
	t.Chdir(t.TempDir())
	writeGoMod(t, `module github.com/wow-look-at-my/repo/writer
go 1.21

require github.com/wow-look-at-my/repo/reader v0.0.0-20200101000000-000000000000

replace github.com/wow-look-at-my/repo/reader => ../reader
`)

	mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)

	changed, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "// go-toolchain:auto-branch", suffixFor(t, "repo/reader v0.0.0"))
	assert.Empty(t, untrackedOrgDeps())
}

// An indirect org require behind a local replace ships its own version to
// consumers too, exactly like the direct case above: the replace is
// main-module-only and names no branch, so the require still has to track.
func TestEnforceOrgBranchTrackingMarksAnIndirectRequireBehindALocalReplacement(t *testing.T) {
	t.Serial()
	t.Chdir(t.TempDir())
	writeGoMod(t, `module test
go 1.21

require github.com/wow-look-at-my/foo v1.2.3 // indirect

replace github.com/wow-look-at-my/foo => ../foo
`)

	mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)

	changed, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "// indirect; go-toolchain:auto-branch", suffixFor(t, "wow-look-at-my/foo v1.2.3"))
}

// The direct sibling and the indirect require behind it get marked in the
// same pass -- neither needs the other to already be tracked.
func TestEnforceOrgBranchTrackingMarksBothSidesOfASiblingPair(t *testing.T) {
	t.Serial()
	t.Chdir(t.TempDir())
	writeGoMod(t, `module github.com/wow-look-at-my/repo/cli
go 1.21

require github.com/wow-look-at-my/repo/validator v0.0.0-20200101000000-000000000000

require github.com/wow-look-at-my/repo/reader v0.0.0-20200101000000-000000000000 // indirect

replace github.com/wow-look-at-my/repo/validator => ../validator

replace github.com/wow-look-at-my/repo/reader => ../reader
`)

	logger.ResetWarnCount()
	mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)

	changed, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Empty(t, logger.EmittedWarnings())
	assert.Equal(t, "// indirect; go-toolchain:auto-branch", suffixFor(t, "repo/reader v0.0.0"))
	assert.Empty(t, untrackedOrgDeps())
}

// There is no pin opt-out left: every org dependency is branch-tracked, so a
// line still carrying the old go-toolchain:pinned comment gets marked same
// as any other unmarked line -- the prose stays, inert, alongside the marker.
func TestEnforceOrgBranchTrackingNoLongerHonorsAPinComment(t *testing.T) {
	t.Serial()
	t.Chdir(t.TempDir())
	writeGoMod(t, `module test
go 1.21

require github.com/wow-look-at-my/foo v1.2.3 // go-toolchain:pinned v2 is a hard API break
`)

	mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)

	changed, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Empty(t, untrackedOrgDeps())
	assert.Contains(t, suffixFor(t, "wow-look-at-my/foo"), autoBranchMarker)
}

// The migration's only question is whether a name repeats the default branch.
// A remote that cannot answer it keeps the name, which follows exactly what
// the line followed before, so an unreachable remote never changes what the
// build resolves. It warns, because a name kept for that reason is a name
// somebody may want to drop later.
func TestEnforceOrgBranchTrackingKeepsTheNameWhenTheRemoteIsUnreachable(t *testing.T) {
	t.Serial()
	t.Chdir(t.TempDir())
	writeGoMod(t, `module test
go 1.21

require github.com/wow-look-at-my/foo v1.2.3 // go-toolchain:branch=master
`)

	logger.ResetWarnCount()
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("git", "ls-remote") {
			return runner.MockProcess(nil, assert.AnError), nil
		}
		return nil, nil
	}

	changed, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "// go-toolchain:auto-branch=master", suffixFor(t, "wow-look-at-my/foo"))

	warnings := logger.EmittedWarnings()
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0].Message, "github.com/wow-look-at-my/foo")
	assert.Contains(t, warnings[0].Message, "=master", "the warning names what to drop")
}

func TestEnforceOrgBranchTrackingIsANoOpWithoutAGoMod(t *testing.T) {
	t.Serial()
	t.Chdir(t.TempDir())

	mock := runner.NewMock()
	changed, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Empty(t, mock.Calls())
}

func TestUntrackedOrgDepsNamesEveryUnmarkedLine(t *testing.T) {
	t.Serial()
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
	t.Serial()
	assert.True(t, isOrgModule("github.com/wow-look-at-my/foo"))
	assert.True(t, isOrgModule("github.com/wow-look-at-my/foo/v2"))
	assert.False(t, isOrgModule("github.com/spf13/cobra"))
	assert.False(t, isOrgModule("github.com/wow-look-at-my-else/foo"))
}
