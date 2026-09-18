package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// lsRemoteURL reports the URL an ls-remote call was aimed at.
func lsRemoteURL(cfg runner.Config) string {
	for _, arg := range cfg.Args {
		if strings.HasPrefix(arg, "https://") {
			return arg
		}
	}
	return ""
}

// WithQuiet() sends git's stderr nowhere, so a bare exit status was the whole
// report: it named neither the repository git worked in nor its objection.
func TestWithGitStderr(t *testing.T) {
	t.Serial()
	base := errors.New("exit status")

	assert.NoError(t, withGitStderr(nil, []byte("not a failure")))
	assert.Equal(t, base, withGitStderr(base, nil))
	assert.Equal(t, base, withGitStderr(base, []byte("  \n\t ")))

	got := withGitStderr(base, []byte("fatal: cannot chdir to nowhere\n"))
	assert.ErrorIs(t, got, base)
	assert.Contains(t, got.Error(), "fatal: cannot chdir to nowhere")
}

// gitOutput hands back stdout, and attaches git's own words to a failure.
func TestGitOutputReportsStdoutAndGitsObjection(t *testing.T) {
	t.Serial()
	mock := runner.NewMock()
	mock.SetResponse("git", []string{"rev-parse", "HEAD"}, []byte("abc123\n"), nil)

	out, err := gitOutput(mock, "git", "rev-parse", "HEAD")
	require.NoError(t, err)
	assert.Equal(t, "abc123", strings.TrimSpace(string(out)))

	failing := runner.NewMock()
	failing.Handler = func(runner.Config) (runner.IProcess, error) {
		return runner.MockProcessWithStderr(nil, []byte("fatal: not a repository\n"), errors.New("exit status 128")), nil
	}
	_, err = gitOutput(failing, "git", "rev-parse", "HEAD")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fatal: not a repository")
}

// A detached HEAD names no branch, and neither does a directory outside any
// repository. Both mean there is nothing to follow.
func TestCurrentBranchIsEmptyWithoutOne(t *testing.T) {
	t.Serial()
	named := runner.NewMock()
	named.SetResponse("git", []string{"rev-parse", "--abbrev-ref", "HEAD"}, []byte("master\n"), nil)
	assert.Equal(t, "master", currentBranch(named))

	detached := runner.NewMock()
	detached.SetResponse("git", []string{"rev-parse", "--abbrev-ref", "HEAD"}, []byte("HEAD\n"), nil)
	assert.Empty(t, currentBranch(detached))

	outside := runner.NewMock()
	outside.Handler = func(runner.Config) (runner.IProcess, error) {
		return runner.MockProcess(nil, errors.New("exit status 128")), nil
	}
	assert.Empty(t, currentBranch(outside))
}

// The answer carries each ref's commit by its full name, and the branch a
// symbolic HEAD stands for.
func TestParseLsRemoteRefsReadsCommitsAndTheSymbolicHead(t *testing.T) {
	t.Serial()
	out := []byte("ref: refs/heads/master\tHEAD\n" +
		"abc123\tHEAD\n" +
		"def456\trefs/heads/master\n" +
		"\n" +
		"789fed\n")

	refs, branch := parseLsRemoteRefs(out)
	assert.Equal(t, "master", branch)
	assert.Equal(t, "abc123", refs["HEAD"])
	assert.Equal(t, "def456", refs["refs/heads/master"])
	assert.Equal(t, "789fed", refs[""], "a lone hash is kept under the empty ref")
}

// The deepest prefix that is a real repository wins, found by asking rather
// than by knowing a host's shape in advance.
func TestResolveGitURLAndRefBacksOffToTheRepositoryRoot(t *testing.T) {
	t.Serial()
	t.Run("a module at the repository root asks once", func(t *testing.T) {
		mock := runner.NewMock()
		mock.SetResponse("git", []string{"ls-remote", "--symref", "https://github.com/wow-look-at-my/api-cli", "HEAD"},
			[]byte("abc123\tHEAD\n"), nil)

		url, output, err := resolveGitURLAndRef(mock, "github.com/wow-look-at-my/api-cli", "HEAD")
		require.NoError(t, err)
		assert.Equal(t, "https://github.com/wow-look-at-my/api-cli", url)
		assert.Contains(t, string(output), "abc123")
		assert.Len(t, mock.Calls(), 1, "a repository root must try no shorter prefix")
	})

	t.Run("a module in a subdirectory backs off", func(t *testing.T) {
		mock := runner.NewMock()
		mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
			switch lsRemoteURL(cfg) {
			case "https://github.com/wow-look-at-my/api-cli/fields":
				return runner.MockProcess(nil, errors.New("exit status 128")), nil
			case "https://github.com/wow-look-at-my/api-cli":
				return runner.MockProcess([]byte("abc123\tHEAD\n"), nil), nil
			}
			t.Fatalf("unexpected ls-remote URL %q", lsRemoteURL(cfg))
			return nil, nil
		}

		url, _, err := resolveGitURLAndRef(mock, "github.com/wow-look-at-my/api-cli/fields", "HEAD")
		require.NoError(t, err)
		assert.Equal(t, "https://github.com/wow-look-at-my/api-cli", url)
	})

	t.Run("every prefix failing reports the earliest error", func(t *testing.T) {
		mock := runner.NewMock()
		mock.Handler = func(runner.Config) (runner.IProcess, error) {
			return runner.MockProcess(nil, errors.New("exit status 128")), nil
		}

		_, _, err := resolveGitURLAndRef(mock, "github.com/wow-look-at-my/api-cli/fields", "HEAD")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "api-cli/fields", "the full module path is what the report names")
	})
}
