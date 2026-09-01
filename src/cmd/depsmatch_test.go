package cmd

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"golang.org/x/mod/modfile"
)

// gitMatchMock is a checkout of `branch` against dependencies whose HEAD points
// at `def` and which carry the full ref names in `has`. It answers ls-remote for
// any repository, records its argv, and supplies the pseudo-version plumbing.
func gitMatchMock(t *testing.T, branch, def, fullHash string, has ...string) (*runner.Mock, *[]string) {
	t.Helper()
	var lsRemote []string
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("git", "rev-parse") {
			return runner.MockProcess([]byte(branch+"\n"), nil), nil
		}
		if cfg.IsCmd("git", "ls-remote") {
			lsRemote = append(lsRemote, strings.Join(cfg.Args[1:], " "))
			return runner.MockProcess(lsRemoteAnswer(cfg.Args, def, fullHash, has), nil), nil
		}
		for _, arg := range cfg.Args {
			if arg == "init" || arg == "fetch" {
				return runner.MockProcess(nil, nil), nil
			}
			if arg == "log" {
				return runner.MockProcess([]byte(strconv.FormatInt(1786567000, 10)+"\n"), nil), nil
			}
		}
		return nil, nil
	}
	return mock, &lsRemote
}

// lsRemoteAnswer writes what the remote reports for the refs an argv asked about.
func lsRemoteAnswer(args []string, def, fullHash string, has []string) []byte {
	var out strings.Builder
	for _, ref := range args[slices.IndexFunc(args, func(a string) bool { return strings.HasPrefix(a, "https://") })+1:] {
		if ref == "HEAD" {
			out.WriteString("ref: refs/heads/" + def + "\tHEAD\n" + fullHash + "\tHEAD\n")
			continue
		}
		if slices.Contains(has, ref) {
			out.WriteString(fullHash + "\t" + ref + "\n")
		}
	}
	return []byte(out.String())
}

// bareMarkerGoMod is an org require carrying the bare marker, the line whose
// resolution the tests below are about.
const bareMarkerGoMod = `module test
go 1.21

require github.com/wow-look-at-my/foo v0.0.0-20200101000000-000000000000 // go-toolchain:auto-branch
`

// A pair of repositories developed in tandem carry the same branch name, so a bare
// marker follows the dependency's copy of it: that is what makes the marker
// resolve to the other half of the change while it is still in flight.
func TestBareMarkerFollowsTheDependencysBranchOfThisRepositorysName(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("go.mod", []byte(bareMarkerGoMod), 0644))

	const fullHash = "351d2159f8d8a85613aa2a6e98c8c63df3c98623"
	mock, lsRemote := gitMatchMock(t, "claude/tandem", "master", fullHash, "refs/heads/claude/tandem")

	changed, err := UpdateTrackedBranchDeps(mock)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Contains(t, *lsRemote, "--symref https://github.com/wow-look-at-my/foo HEAD refs/heads/claude/tandem",
		"the matching branch and the default branch are one question, not two round trips")
	assert.Contains(t, *lsRemote, "https://github.com/wow-look-at-my/foo refs/heads/claude/tandem",
		"the matched branch is what the version is then resolved against")
}

// The merge that lands the change deletes the branch, and that is also what
// makes the match stop matching -- so nothing has to be repointed by hand.
func TestBareMarkerFallsBackToTheDefaultBranchWhenTheDependencyLacksIt(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("go.mod", []byte(bareMarkerGoMod), 0644))

	const fullHash = "351d2159f8d8a85613aa2a6e98c8c63df3c98623"
	mock, lsRemote := gitMatchMock(t, "claude/tandem", "master", fullHash)

	changed, err := UpdateTrackedBranchDeps(mock)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.NotContains(t, strings.Join(*lsRemote, "\n"), "https://github.com/wow-look-at-my/foo refs/heads/claude/tandem",
		"a branch the dependency does not have is not a branch to follow")
	assert.Contains(t, *lsRemote, "--symref https://github.com/wow-look-at-my/foo HEAD")
}

// Naming the branch a dependency's HEAD already points at resolves to the same
// commit, so it stays HEAD: the marker's whole point is not writing the name down.
func TestBareMarkerStaysOnHeadWhenTheMatchIsTheDefaultBranch(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("go.mod", []byte(bareMarkerGoMod), 0644))

	const fullHash = "351d2159f8d8a85613aa2a6e98c8c63df3c98623"
	mock, lsRemote := gitMatchMock(t, "master", "master", fullHash, "refs/heads/master")

	changed, err := UpdateTrackedBranchDeps(mock)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.NotContains(t, strings.Join(*lsRemote, "\n"), "foo refs/heads/master")
}

// A named marker is a deliberate, permanent choice: the answer must not depend
// on which branch the reader happens to be standing on.
func TestANamedMarkerIsNeverMatchedAgainstThisRepositorysBranch(t *testing.T) {
	t.Chdir(t.TempDir())
	gomod := `module test
go 1.21

require github.com/wow-look-at-my/foo v0.0.0-20200101000000-000000000000 // go-toolchain:auto-branch=v1
`
	require.NoError(t, os.WriteFile("go.mod", []byte(gomod), 0644))

	const fullHash = "351d2159f8d8a85613aa2a6e98c8c63df3c98623"
	mock, lsRemote := gitMatchMock(t, "claude/tandem", "master", fullHash, "refs/heads/claude/tandem", "refs/heads/v1")

	_, err := UpdateTrackedBranchDeps(mock)
	require.NoError(t, err)
	assert.Equal(t, []string{"https://github.com/wow-look-at-my/foo refs/heads/v1"}, *lsRemote,
		"a named branch is resolved directly, with nothing to probe")
}

// A detached HEAD is what CI hands a pull-request build, and there is no branch
// name in it to match anything against.
func TestCurrentBranchIsEmptyOnADetachedHead(t *testing.T) {
	t.Parallel()
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		return runner.MockProcess([]byte("HEAD\n"), nil), nil
	}
	assert.Empty(t, currentBranch(mock))
}

// Outside a repository there is nothing to ask, and a bare marker keeps the
// behaviour it had before matching existed.
func TestCurrentBranchIsEmptyOutsideARepository(t *testing.T) {
	t.Parallel()
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		return runner.MockProcess(nil, os.ErrNotExist), nil
	}
	assert.Empty(t, currentBranch(mock))
}

// The fast-exit check and the rewrite have to agree about which ref a line
// follows, or the cache reports up-to-date against a commit the run would move.
func TestTrackedBranchDepsMovedFollowsTheMatchingBranchToo(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("go.mod", []byte(bareMarkerGoMod), 0644))

	mock, lsRemote := gitMatchMock(t, "claude/tandem", "master",
		"351d2159f8d8a85613aa2a6e98c8c63df3c98623", "refs/heads/claude/tandem")

	assert.True(t, trackedBranchDepsMoved(mock))
	assert.Contains(t, *lsRemote, "https://github.com/wow-look-at-my/foo refs/heads/claude/tandem")
}

// A repository is asked a single time however many of its modules go.mod requires.
func TestTheMatchIsProbedOncePerModule(t *testing.T) {
	t.Chdir(t.TempDir())
	gomod := `module test
go 1.21

require github.com/wow-look-at-my/foo v0.0.0-20200101000000-000000000000 // go-toolchain:auto-branch

replace example.com/bar => github.com/wow-look-at-my/foo v0.0.0-20200101000000-000000000000 // go-toolchain:auto-branch
`
	require.NoError(t, os.WriteFile("go.mod", []byte(gomod), 0644))

	mock, lsRemote := gitMatchMock(t, "claude/tandem", "master",
		"351d2159f8d8a85613aa2a6e98c8c63df3c98623", "refs/heads/claude/tandem")

	_, err := UpdateTrackedBranchDeps(mock)
	require.NoError(t, err)

	probes := 0
	for _, call := range *lsRemote {
		if strings.HasPrefix(call, "--symref") {
			probes++
		}
	}
	assert.Equal(t, 1, probes)
}

// The matched branch is a resolution, never a rewrite: writing the name into
// go.mod would leave a pin at a branch the next merge deletes.
func TestAMatchedBranchIsNeverWrittenIntoGoMod(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("go.mod", []byte(bareMarkerGoMod), 0644))

	mock, _ := gitMatchMock(t, "claude/tandem", "master",
		"351d2159f8d8a85613aa2a6e98c8c63df3c98623", "refs/heads/claude/tandem")

	_, err := UpdateTrackedBranchDeps(mock)
	require.NoError(t, err)

	f, err := modfile.Parse("go.mod", readGoMod(t), nil)
	require.NoError(t, err)
	assert.Equal(t, marker{tracks: true}, parseMarker(f.Require[0].Syntax))
}
