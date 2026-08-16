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
	example.com/sib v1.0.0 // indirect; go-toolchain:auto-branch
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
	assert.Equal(t, marker{tracks: true}, got["example.com/sib"], "the marker is still found sharing a comment with // indirect")
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
	assert.Equal(t, "go-toolchain:branch=master", marker{tracks: true, branch: "master", legacy: true}.comment(),
		"a line read in the old spelling is written back in it, until every runner reads the new one")
}

// auto-branch is READ here and written by the next release, so a line already
// carrying it is left exactly as it is -- including asking the remote nothing.
func TestEnforceOrgBranchTrackingLeavesAnAutoBranchLineAlone(t *testing.T) {
	for _, comment := range []string{"go-toolchain:auto-branch", "go-toolchain:auto-branch=v1"} {
		t.Run(comment, func(t *testing.T) {
			t.Chdir(t.TempDir())
			writeGoMod(t, "module test\ngo 1.21\n\nrequire github.com/wow-look-at-my/foo v1.2.3 // "+comment+"\n")

			mock, _ := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)
			changed, err := EnforceOrgBranchTracking(mock)
			require.NoError(t, err)
			assert.False(t, changed)
			assert.Empty(t, mock.Calls(), "already tracked, so nothing to ask")
		})
	}
}

// Writing the new spelling has to wait for a fleet that reads it. Until then
// an unmarked line gets the one every release understands, so a repo built by
// an older binary and a newer one lands on the same go.mod either way.
func TestEnforceOrgBranchTrackingStillWritesTheLegacySpelling(t *testing.T) {
	t.Chdir(t.TempDir())
	gomod := `module test
go 1.25.0

require github.com/wow-look-at-my/foo v1.2.3
`
	require.NoError(t, os.WriteFile("go.mod", []byte(gomod), 0644))

	mock, lsRemote := defaultBranchMock(t, "master", "351d2159f8d8a85613aa2a6e98c8c63df3c98623", 1786567000)
	changed, err := EnforceOrgBranchTracking(mock)
	require.NoError(t, err)
	assert.True(t, changed)

	assert.Equal(t, "// go-toolchain:branch=master", suffixFor(t, "wow-look-at-my/foo"))
	assert.Contains(t, strings.Join(*lsRemote, "\n"), "--symref", "the written marker names a branch, so it has to be asked for")
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

// A marker joins the comment already on the line rather than becoming a second
// one: modfile renders an extra Suffix comment underneath, and a marker on its
// own line above the next require is what corrupts the block.
func TestSetMarkerJoinsAnExistingComment(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
		want string
	}{
		{"no comment", "require example.com/a v1.0.0", "// go-toolchain:auto-branch"},
		{"indirect", "require example.com/a v1.0.0 // indirect", "// indirect; go-toolchain:auto-branch"},
		{"replacing a marker", "require example.com/a v1.0.0 // go-toolchain:branch=v1", "// go-toolchain:auto-branch"},
		{"replacing a marker beside indirect", "require example.com/a v1.0.0 // indirect; go-toolchain:branch=v1", "// indirect; go-toolchain:auto-branch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := modfile.Parse("go.mod", []byte("module test\ngo 1.25.0\n\n"+tc.line+"\n"), nil)
			require.NoError(t, err)

			setMarker(f.Require[0].Syntax, marker{tracks: true})
			out, err := f.Format()
			require.NoError(t, err)

			var got []string
			for _, line := range strings.Split(string(out), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "//") {
					got = append(got, strings.TrimSpace(line))
				}
			}
			assert.Empty(t, got, "no comment may end up on a line of its own:\n%s", out)
			assert.Contains(t, string(out), tc.want)
		})
	}
}
