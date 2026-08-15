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
)

const (
	siblingHash  = "d8426ef8d505327f80fe9c42f0645838528d9786"
	siblingEpoch = int64(1786567000)
	// The pseudo-version every module in the repository gets at siblingHash.
	siblingVersion = "v0.0.0-20260812203640-d8426ef8d505"
)

// repoURL is the one URL in these tests that is a repository. A module inside
// it has a longer import path than the repository has a URL, so ls-remote must
// fail for the deeper URLs the way a host does -- that failure is what tells
// resolveGitURLAndRef where the repository stops and the subdirectory starts.
const repoURL = "https://github.com/wow-look-at-my/common-ai-api"

// repoTreeMock answers ls-remote for repoURL, the plumbing fetchCommit needs,
// and `git cat-file -p <hash>:<path>` from files, keyed by repository-relative
// path. A path the map does not hold fails the way git does for one the commit
// does not carry.
func repoTreeMock(t *testing.T, files map[string]string) *runner.Mock {
	t.Helper()
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("git", "ls-remote") {
			url := cfg.Args[len(cfg.Args)-2]
			if url != repoURL {
				return runner.MockProcess(nil, errors.New("repository not found: "+url)), nil
			}
			for _, arg := range cfg.Args {
				if arg == "--symref" {
					return runner.MockProcess([]byte("ref: refs/heads/master\tHEAD\n"+siblingHash+"\tHEAD\n"), nil), nil
				}
			}
			return runner.MockProcess([]byte(siblingHash+"\trefs/heads/master\n"), nil), nil
		}
		for i, arg := range cfg.Args {
			switch arg {
			case "init", "fetch":
				return runner.MockProcess(nil, nil), nil
			case "log":
				return runner.MockProcess([]byte(strconv.FormatInt(siblingEpoch, 10)+"\n"), nil), nil
			case "cat-file":
				spec := cfg.Args[len(cfg.Args)-1]
				_, rel, _ := strings.Cut(spec, ":")
				body, ok := files[rel]
				if !ok {
					return runner.MockProcess(nil, errors.New("path does not exist in "+cfg.Args[i])), nil
				}
				return runner.MockProcess([]byte(body), nil), nil
			}
		}
		return nil, nil
	}
	return mock
}

// The modules of one repository, as they pin each other: client's require on
// core names an OLDER commit than the one being published, because the commit
// publishing them both did not exist when the line was written.
func commonAPITree() map[string]string {
	return map[string]string{
		"go/client/go.mod": `module github.com/wow-look-at-my/common-ai-api/go/client

go 1.25.0

require (
	github.com/wow-look-at-my/common-ai-api/go/core v0.0.0-20260101000000-000000000000 // go-toolchain:branch=master
	github.com/stretchr/testify v1.11.1
)
`,
		"go/core/go.mod": `module github.com/wow-look-at-my/common-ai-api/go/core

go 1.25.0

require github.com/wow-look-at-my/xml-validator v0.0.0-20260101000000-000000000000 // go-toolchain:branch=master
`,
	}
}

func TestSiblingRequiresWalksTheRepositoryAtOneCommit(t *testing.T) {
	mock := repoTreeMock(t, commonAPITree())
	c, cleanup, err := fetchCommit(mock, "github.com/wow-look-at-my/common-ai-api/go/client", "refs/heads/master")
	require.NoError(t, err)
	defer cleanup()

	assert.Equal(t, "github.com/wow-look-at-my/common-ai-api", c.RepoRoot)
	assert.Equal(t, "go/client", c.Subdir)

	sibs, err := siblingRequires(mock, c, "example.com/consumer")
	require.NoError(t, err)

	// core is in the same repository, so it comes along at THIS commit, not at
	// the one client's own go.mod names. testify and xml-validator are other
	// repositories and are left to their own lines.
	assert.Equal(t, map[string]string{
		"github.com/wow-look-at-my/common-ai-api/go/core": siblingVersion,
	}, sibs)
}

func TestSiblingRequiresNeverRequiresTheMainModule(t *testing.T) {
	mock := repoTreeMock(t, commonAPITree())
	c, cleanup, err := fetchCommit(mock, "github.com/wow-look-at-my/common-ai-api/go/client", "refs/heads/master")
	require.NoError(t, err)
	defer cleanup()

	sibs, err := siblingRequires(mock, c, "github.com/wow-look-at-my/common-ai-api/go/core")
	require.NoError(t, err)
	assert.Empty(t, sibs, "a module cannot require itself")
}

func TestSiblingRequiresFailsWhenTheCommitDoesNotCarryTheModule(t *testing.T) {
	tree := commonAPITree()
	delete(tree, "go/core/go.mod")
	mock := repoTreeMock(t, tree)
	c, cleanup, err := fetchCommit(mock, "github.com/wow-look-at-my/common-ai-api/go/client", "refs/heads/master")
	require.NoError(t, err)
	defer cleanup()

	_, err = siblingRequires(mock, c, "example.com/consumer")
	require.Error(t, err, "a sibling missing at the resolved commit is the bug this prevents, not something to skip")
	assert.Contains(t, err.Error(), "go/core/go.mod")
}

func TestInRepo(t *testing.T) {
	const root = "github.com/wow-look-at-my/common-ai-api"
	assert.True(t, inRepo(root, root))
	assert.True(t, inRepo(root+"/go/core", root))
	assert.False(t, inRepo("github.com/wow-look-at-my/common-ai-api-extras", root), "a prefix that stops mid-segment is another repository")
	assert.False(t, inRepo("github.com/wow-look-at-my/agentic-loop/go", root))
}

func TestModuleSubdir(t *testing.T) {
	const root = "github.com/org/repo"
	assert.Equal(t, "", moduleSubdir(root, root))
	assert.Equal(t, "go/core", moduleSubdir(root+"/go/core", root))
	assert.Equal(t, "go/core", moduleSubdir(root+"/go/core/v2", root), "a major-version suffix names no directory")
	assert.Equal(t, "", moduleSubdir(root+"/v3", root))
}

// The end-to-end shape: a consumer tracking one module of a multi-module repo
// ends up requiring the whole repo at one commit, so the stale pin inside the
// dependency loses minimal version selection and is never fetched.
func TestUpdateTrackedBranchDepsRequiresSiblingsAtTheSameCommit(t *testing.T) {
	t.Chdir(t.TempDir())
	gomod := `module example.com/consumer

go 1.25.0

require github.com/wow-look-at-my/common-ai-api/go/client v0.0.0-20260101000000-000000000000 // go-toolchain:auto-branch
`
	require.NoError(t, os.WriteFile("go.mod", []byte(gomod), 0644))

	changed, err := UpdateTrackedBranchDeps(repoTreeMock(t, commonAPITree()))
	require.NoError(t, err)
	assert.True(t, changed)

	f, err := modfile.Parse("go.mod", readGoMod(t), nil)
	require.NoError(t, err)

	core := findRequire(f, "github.com/wow-look-at-my/common-ai-api/go/core")
	require.NotNil(t, core, "the sibling has to be required here: a replace would not travel to this module's own consumers")
	assert.Equal(t, siblingVersion, core.Mod.Version)
	assert.Equal(t, marker{tracks: true}, parseMarker(core.Syntax), "the added line follows what the line that brought it in follows, so later runs keep moving it")

	client := findRequire(f, "github.com/wow-look-at-my/common-ai-api/go/client")
	require.NotNil(t, client)
	assert.Equal(t, siblingVersion, client.Mod.Version)
}

func TestUpdateTrackedBranchDepsLeavesADeliberatelyPinnedSiblingAlone(t *testing.T) {
	t.Chdir(t.TempDir())
	gomod := `module example.com/consumer

go 1.25.0

require (
	github.com/wow-look-at-my/common-ai-api/go/client v0.0.0-20260101000000-000000000000 // go-toolchain:auto-branch
	github.com/wow-look-at-my/common-ai-api/go/core v0.0.0-20250101000000-111111111111 // go-toolchain:pinned last release before the API break
)
`
	require.NoError(t, os.WriteFile("go.mod", []byte(gomod), 0644))

	_, err := UpdateTrackedBranchDeps(repoTreeMock(t, commonAPITree()))
	require.NoError(t, err)

	f, err := modfile.Parse("go.mod", readGoMod(t), nil)
	require.NoError(t, err)
	core := findRequire(f, "github.com/wow-look-at-my/common-ai-api/go/core")
	require.NotNil(t, core)
	assert.Equal(t, "v0.0.0-20250101000000-111111111111", core.Mod.Version, "moving with its siblings is what the pinned marker opts out of")
}

// A sibling that tidy has since marked indirect is this run's own line, so it
// keeps being resolved rather than drawing the warning meant for a branch
// marker someone put on a transitively resolved dependency.
func TestUpdateTrackedBranchDepsResolvesAnIndirectSibling(t *testing.T) {
	t.Chdir(t.TempDir())
	gomod := `module example.com/consumer

go 1.25.0

require github.com/wow-look-at-my/common-ai-api/go/client v0.0.0-20260101000000-000000000000 // go-toolchain:auto-branch

require github.com/wow-look-at-my/common-ai-api/go/core v0.0.0-20260101000000-000000000000 // indirect; go-toolchain:branch=master
`
	require.NoError(t, os.WriteFile("go.mod", []byte(gomod), 0644))

	logger.ResetWarnCount()
	defer logger.ResetWarnCount()

	changed, err := UpdateTrackedBranchDeps(repoTreeMock(t, commonAPITree()))
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, int64(0), logger.WarnCount(), "the line is this run's own, not a marker someone misplaced")

	f, err := modfile.Parse("go.mod", readGoMod(t), nil)
	require.NoError(t, err)
	core := findRequire(f, "github.com/wow-look-at-my/common-ai-api/go/core")
	require.NotNil(t, core)
	assert.Equal(t, siblingVersion, core.Mod.Version)
}

// Two DIRECT modules of one repository is the case that needs the resolver:
// resolving each on its own would ask the remote twice, and a branch that
// moved between the two answers would land them on different commits. Which
// modules share a repository is read off the repository, so neither line has
// to say anything about the other.
func TestUpdateTrackedBranchDepsResolvesEachRepositoryOnce(t *testing.T) {
	t.Chdir(t.TempDir())
	gomod := `module example.com/consumer

go 1.25.0

require (
	github.com/wow-look-at-my/common-ai-api/go/client v0.0.0-20260101000000-000000000000 // go-toolchain:auto-branch
	github.com/wow-look-at-my/common-ai-api/go/core v0.0.0-20260101000000-000000000000 // go-toolchain:auto-branch
)
`
	require.NoError(t, os.WriteFile("go.mod", []byte(gomod), 0644))

	mock := repoTreeMock(t, commonAPITree())
	changed, err := UpdateTrackedBranchDeps(mock)
	require.NoError(t, err)
	assert.True(t, changed)

	resolutions := 0
	for _, call := range mock.Calls() {
		if call.IsCmd("git", "ls-remote") && lsRemoteURL(call) == repoURL {
			resolutions++
		}
	}
	assert.Equal(t, 1, resolutions, "one repository is one answer, however many of its modules ask")

	f, err := modfile.Parse("go.mod", readGoMod(t), nil)
	require.NoError(t, err)
	for _, mod := range []string{"go/client", "go/core"} {
		req := findRequire(f, "github.com/wow-look-at-my/common-ai-api/"+mod)
		require.NotNil(t, req)
		assert.Equal(t, siblingVersion, req.Mod.Version, mod+" lands on the commit its repository resolved to")
	}
}
