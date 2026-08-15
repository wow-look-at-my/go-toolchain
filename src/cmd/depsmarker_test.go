package cmd

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"golang.org/x/mod/modfile"
)

func TestParseMarker(t *testing.T) {
	gomod := `module test
go 1.25.0

require (
	example.com/def v1.0.0 // go-toolchain:auto-branch
	example.com/named v1.0.0 // go-toolchain:auto-branch=v1
	example.com/sib v1.0.0 // indirect; go-toolchain:sibling=example.com/anchor
	example.com/old v1.0.0 // go-toolchain:branch=master
	example.com/plain v1.0.0
	example.com/pinned v1.0.0 // go-toolchain:pinned v2 API break
)
`
	f, err := modfile.Parse("go.mod", []byte(gomod), nil)
	require.NoError(t, err)

	got := map[string]marker{}
	for _, req := range f.Require {
		got[req.Mod.Path] = parseMarker(req.Syntax)
	}

	assert.Equal(t, marker{tracks: true}, got["example.com/def"], "bare auto-branch names no branch, which is the point")
	assert.Equal(t, marker{tracks: true, branch: "v1"}, got["example.com/named"])
	assert.Equal(t, marker{tracks: true, sibling: "example.com/anchor"}, got["example.com/sib"])
	assert.Equal(t, marker{tracks: true, branch: "master", legacy: true}, got["example.com/old"])
	assert.Equal(t, marker{}, got["example.com/plain"])
	assert.Equal(t, marker{}, got["example.com/pinned"], "a deliberate pin is a choice, not a tracked answer")
}

func TestMarkerRefIsHeadWhenItNamesNoBranch(t *testing.T) {
	assert.Equal(t, "HEAD", marker{tracks: true}.ref(), "HEAD IS the default branch, so following it costs no extra lookup")
	assert.Equal(t, "refs/heads/v1", marker{tracks: true, branch: "v1"}.ref())
}

func TestMarkerComment(t *testing.T) {
	assert.Equal(t, "go-toolchain:auto-branch", marker{tracks: true}.comment())
	assert.Equal(t, "go-toolchain:auto-branch=v1", marker{tracks: true, branch: "v1"}.comment())
	assert.Equal(t, "go-toolchain:sibling=example.com/a", marker{tracks: true, sibling: "example.com/a"}.comment())
}

// An unmarked org require gets the bare marker and makes NO network call: the
// branch it follows is whatever the remote says is default, asked at
// resolution time rather than copied into go.mod now.
func TestEnforceOrgBranchTrackingWritesTheBareMarker(t *testing.T) {
	t.Chdir(t.TempDir())
	gomod := `module test
go 1.25.0

require github.com/wow-look-at-my/foo v1.2.3
`
	require.NoError(t, os.WriteFile("go.mod", []byte(gomod), 0644))

	mock := repoTreeMock(t, commonAPITree())
	changed, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Empty(t, mock.Calls(), "naming no branch means asking nothing")

	f, err := modfile.Parse("go.mod", readGoMod(t), nil)
	require.NoError(t, err)
	assert.Equal(t, marker{tracks: true}, parseMarker(findRequire(f, "github.com/wow-look-at-my/foo").Syntax))
}

// A release that predates auto-branch does not recognize it, reads the line as
// untracked, and appends its own marker as a standalone comment ABOVE the
// require -- which corrupts the block. So a legacy line is read and left, and
// the new spelling only ever lands on a line that had no marker at all.
func TestEnforceOrgBranchTrackingLeavesTheLegacyMarkerAlone(t *testing.T) {
	t.Chdir(t.TempDir())
	gomod := "module test\ngo 1.25.0\n\nrequire github.com/wow-look-at-my/foo v1.2.3 // go-toolchain:branch=master\n"
	require.NoError(t, os.WriteFile("go.mod", []byte(gomod), 0644))

	mock := repoTreeMock(t, commonAPITree())
	changed, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Empty(t, mock.Calls(), "there is nothing to decide, so nothing to ask")
	assert.Contains(t, string(readGoMod(t)), "// go-toolchain:branch=master")
}

func TestEnforceOrgBranchTrackingLeavesTheCanonicalFormAlone(t *testing.T) {
	t.Chdir(t.TempDir())
	gomod := `module test
go 1.25.0

require github.com/wow-look-at-my/foo v1.2.3 // go-toolchain:auto-branch
`
	require.NoError(t, os.WriteFile("go.mod", []byte(gomod), 0644))

	changed, err := EnforceOrgBranchTracking(repoTreeMock(t, commonAPITree()))
	require.NoError(t, err)
	assert.False(t, changed)
}

func TestGitHubOwnerRepo(t *testing.T) {
	owner, repo, ok := gitHubOwnerRepo("github.com/wow-look-at-my/common-ai-api/go/client")
	assert.True(t, ok)
	assert.Equal(t, "wow-look-at-my", owner)
	assert.Equal(t, "common-ai-api", repo, "on GitHub the repository is the first three segments, subdirectory module or not")

	_, _, ok = gitHubOwnerRepo("gitlab.com/group/sub/project")
	assert.False(t, ok, "only GitHub has the API this guard asks")
}

// The guard is the answer to a specific accident: point a pin at the branch
// you are about to merge, watch CI go green, merge, and the branch is gone.
func TestReportTemporaryBranchesFailsInCIAndWarnsOutsideIt(t *testing.T) {
	found := []temporaryBranch{{module: "github.com/org/repo", branch: "claude/wip", pr: "https://github.com/org/repo/pull/7"}}

	t.Setenv("CI", "")
	assert.NoError(t, reportTemporaryBranches(found), "developing two repos in tandem is real; the warning is the reminder")

	t.Setenv("CI", "true")
	err := reportTemporaryBranches(found)
	require.Error(t, err, "CI is the last look before the merge that deletes the branch")
	assert.Contains(t, err.Error(), "pull/7")
	assert.Contains(t, err.Error(), "claude/wip")

	assert.NoError(t, reportTemporaryBranches(nil))
}

// The run's own token is scoped to the repository being built, so a
// cross-repository check against a private one is refused as a matter of
// course. Saying so once names every branch it covers; saying it per line
// would fire on every run of every repo forever.
func TestReportUncheckedBranchesSaysItOnce(t *testing.T) {
	logger.ResetWarnCount()
	defer logger.ResetWarnCount()

	reportUncheckedBranches(nil)
	assert.Equal(t, int64(0), logger.WarnCount(), "nothing unchecked is nothing to say")

	reportUncheckedBranches([]string{"github.com/org/a@v1", "github.com/org/b@v1"})
	warnings := logger.EmittedWarnings()
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "github.com/org/a@v1")
	assert.Contains(t, warnings[0], "github.com/org/b@v1")
}
