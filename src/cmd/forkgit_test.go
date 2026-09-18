package cmd

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// lsRemoteURL is the repository an ls-remote argv asked about.
func lsRemoteURL(cfg runner.Config) string {
	for _, arg := range cfg.Args {
		if strings.HasPrefix(arg, "https://") {
			return arg
		}
	}
	return ""
}

// A detached HEAD is what CI hands a pull-request build, and it carries no
// branch name.
func TestCurrentBranchIsEmptyOnADetachedHead(t *testing.T) {
	t.Serial()
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		return runner.MockProcess([]byte("HEAD\n"), nil), nil
	}
	assert.Empty(t, currentBranch(mock))
}

// Outside a repository there is nothing to ask.
func TestCurrentBranchIsEmptyOutsideARepository(t *testing.T) {
	t.Serial()
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		return runner.MockProcess(nil, os.ErrNotExist), nil
	}
	assert.Empty(t, currentBranch(mock))
}

func TestCurrentBranchNamesTheCheckedOutBranch(t *testing.T) {
	t.Serial()
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		return runner.MockProcess([]byte("claude/tandem\n"), nil), nil
	}
	assert.Equal(t, "claude/tandem", currentBranch(mock))
}

func TestResolveGitURLAndRef(t *testing.T) {
	t.Serial()
	t.Run("module at repo root resolves on the first try", func(t *testing.T) {
		mock := runner.NewMock()
		mock.SetResponse("git", []string{"ls-remote", "--symref", "https://github.com/wow-look-at-my/gosmopolitan", "HEAD"},
			[]byte("abc123\tHEAD\n"), nil)

		url, output, err := resolveGitURLAndRef(mock, "github.com/wow-look-at-my/gosmopolitan", "HEAD")
		require.NoError(t, err)
		assert.Equal(t, "https://github.com/wow-look-at-my/gosmopolitan", url)
		assert.Contains(t, string(output), "abc123")
		assert.Len(t, mock.Calls(), 1, "the repo-root case must not try any shorter prefix")
	})

	// The deepest prefix that IS a repository wins, found by trying rather than
	// by a table of hosts.
	t.Run("module in a subdirectory backs off to the repo root", func(t *testing.T) {
		mock := runner.NewMock()
		mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
			require.True(t, cfg.IsCmd("git", "ls-remote"))
			switch lsRemoteURL(cfg) {
			case "https://github.com/wow-look-at-my/gosmopolitan/go":
				return runner.MockProcess(nil, errors.New("exit status 128")), nil
			case "https://github.com/wow-look-at-my/gosmopolitan":
				return runner.MockProcess([]byte("abc123\tHEAD\n"), nil), nil
			}
			t.Fatalf("unexpected ls-remote URL %q", lsRemoteURL(cfg))
			return nil, nil
		}

		url, _, err := resolveGitURLAndRef(mock, "github.com/wow-look-at-my/gosmopolitan/go", "HEAD")
		require.NoError(t, err)
		assert.Equal(t, "https://github.com/wow-look-at-my/gosmopolitan", url)
	})

	// The full module path is the error worth reporting: a shorter prefix fails
	// for a reason the caller never asked about.
	t.Run("total failure reports the earliest error", func(t *testing.T) {
		mock := runner.NewMock()
		mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
			if lsRemoteURL(cfg) == "https://example.com/repo/sub" {
				return runner.MockProcess(nil, os.ErrNotExist), nil
			}
			return runner.MockProcess(nil, errors.New("exit status 128")), nil
		}

		_, _, err := resolveGitURLAndRef(mock, "example.com/repo/sub", "HEAD")
		require.Error(t, err)
		assert.ErrorIs(t, err, os.ErrNotExist)
	})
}

// A symref answer carries the default branch beside the commits, which is how
// a single question covers both the branch to follow and the fallback.
func TestParseLsRemoteRefs(t *testing.T) {
	t.Serial()
	out := []byte("ref: refs/heads/master\tHEAD\n" +
		"351d2159f8d8a85613aa2a6e98c8c63df3c98623\tHEAD\n" +
		"9f1c0d8b7a6e5d4c3b2a1908f7e6d5c4b3a29180\trefs/heads/claude/tandem\n")

	refs, branch := parseLsRemoteRefs(out)
	assert.Equal(t, "master", branch)
	assert.Equal(t, "351d2159f8d8a85613aa2a6e98c8c63df3c98623", refs["HEAD"])
	assert.Equal(t, "9f1c0d8b7a6e5d4c3b2a1908f7e6d5c4b3a29180", refs["refs/heads/claude/tandem"])
	assert.Empty(t, refs["refs/heads/absent"])
}

func TestParseLsRemoteRefsReadsAnEmptyAnswer(t *testing.T) {
	t.Serial()
	refs, branch := parseLsRemoteRefs(nil)
	assert.Empty(t, branch)
	assert.Empty(t, refs)
}

// WithQuiet() sends stderr nowhere, so a bare exit status was the whole report
// until what git said is attached to it.
func TestWithGitStderrCarriesWhatGitSaid(t *testing.T) {
	t.Serial()
	err := withGitStderr(errors.New("exit status 128"), []byte("fatal: repository not found\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit status 128")
	assert.Contains(t, err.Error(), "fatal: repository not found")

	assert.NoError(t, withGitStderr(nil, []byte("warning: whatever")))
}

func TestGitOutputReturnsStdout(t *testing.T) {
	t.Serial()
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		return runner.MockProcess([]byte("351d2159f8d8a85613aa2a6e98c8c63df3c98623\n"), nil), nil
	}

	out, err := gitOutput(mock, "git", "rev-parse", "HEAD")
	require.NoError(t, err)
	assert.Equal(t, "351d2159f8d8a85613aa2a6e98c8c63df3c98623", strings.TrimSpace(string(out)))
}
