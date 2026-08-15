package cmd

import (
	"os"
	"strings"
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

// An unmarked org require gets auto-branch plus the compatibility half, which
// is what an older release reads to leave the line alone.
func TestEnforceOrgBranchTrackingWritesTheBridgedMarker(t *testing.T) {
	t.Chdir(t.TempDir())
	gomod := `module test
go 1.25.0

require github.com/wow-look-at-my/foo v1.2.3
`
	require.NoError(t, os.WriteFile("go.mod", []byte(gomod), 0644))

	mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)
	changed, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	assert.True(t, changed)

	assert.Equal(t, "// go-toolchain:auto-branch go-toolchain:branch=master", suffixFor(t, "wow-look-at-my/foo"))
	f, err := modfile.Parse("go.mod", readGoMod(t), nil)
	require.NoError(t, err)
	assert.Equal(t, marker{tracks: true, compat: "master"}, parseMarker(findRequire(f, "github.com/wow-look-at-my/foo").Syntax))
}

// The legacy marker is what an older release reads, so the migration KEEPS it,
// last on the line and with nothing after it: that release takes everything
// following "branch=" as the branch name. Both readers then answer correctly
// off one line, and neither overwrites the other -- without which an older one
// treats the line as unmarked and appends a comment of its own on a line of
// its own, corrupting the require block.
func TestEnforceOrgBranchTrackingMigratesTheLegacyMarker(t *testing.T) {
	for _, tc := range []struct {
		name  string
		was   string
		want  string
		about string
	}{
		{"default branch", "master", "// go-toolchain:auto-branch go-toolchain:branch=master", "the name only repeated the default branch, so the new half stops naming it"},
		{"other branch", "v1", "// go-toolchain:auto-branch=v1 go-toolchain:branch=v1", "a deliberate non-default branch is a choice both halves keep"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			writeGoMod(t, "module test\ngo 1.21\n\nrequire github.com/wow-look-at-my/foo v1.2.3 // go-toolchain:branch="+tc.was+"\n")

			mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)
			changed, err := EnforceOrgBranchTracking(mock)
			require.NoError(t, err)
			assert.True(t, changed)
			assert.Equal(t, tc.want, suffixFor(t, "wow-look-at-my/foo"), tc.about)
		})
	}
}

// An older release reads the compatibility half and sees a tracked line, so it
// appends nothing. Proven against that release's own parser: everything after
// "branch=" to the end of the comment, which is why nothing may follow it.
func TestBridgedMarkerReadsCorrectlyToAnOlderRelease(t *testing.T) {
	for _, tc := range []struct {
		m    marker
		want string
	}{
		{marker{tracks: true, compat: "master"}, "master"},
		{marker{tracks: true, branch: "v1", compat: "v1"}, "v1"},
		{marker{tracks: true, sibling: "example.com/anchor", compat: "master"}, "master"},
	} {
		token := "// " + tc.m.comment()
		i := strings.Index(token, legacyBranchMarker)
		require.NotEqual(t, -1, i, "an older release finds nothing to follow in %q", token)
		assert.Equal(t, tc.want, strings.TrimSpace(token[i+len(legacyBranchMarker):]), "older parser reading %q", token)
	}
}

func TestEnforceOrgBranchTrackingLeavesTheBridgedFormAlone(t *testing.T) {
	t.Chdir(t.TempDir())
	gomod := `module test
go 1.25.0

require github.com/wow-look-at-my/foo v1.2.3 // go-toolchain:auto-branch go-toolchain:branch=master
`
	require.NoError(t, os.WriteFile("go.mod", []byte(gomod), 0644))

	mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)
	changed, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Empty(t, mock.Calls(), "already canonical, so nothing to ask")
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
