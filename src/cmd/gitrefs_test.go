package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

func TestParseLsRemoteRefs(t *testing.T) {
	out := []byte("ref: refs/heads/master\tHEAD\n" +
		"5bf638fdce71aa0d6a3b2a8bdb9a4a7f3c2d1e0f\tHEAD\n" +
		"5bf638fdce71aa0d6a3b2a8bdb9a4a7f3c2d1e0f\trefs/heads/master\n" +
		"aa11bb22cc33dd44ee55ff6600778899aabbccdd\trefs/heads/v1\n")

	refs, branch := parseLsRemoteRefs(out)
	assert.Equal(t, "master", branch)
	assert.Equal(t, "5bf638fdce71aa0d6a3b2a8bdb9a4a7f3c2d1e0f", refs["HEAD"])
	assert.Equal(t, "5bf638fdce71aa0d6a3b2a8bdb9a4a7f3c2d1e0f", refs["refs/heads/master"])
	assert.Equal(t, "aa11bb22cc33dd44ee55ff6600778899aabbccdd", refs["refs/heads/v1"])
}

func TestParseLsRemoteRefsWithoutASymbolicHead(t *testing.T) {
	refs, branch := parseLsRemoteRefs([]byte("aa11bb22cc33dd44ee55ff6600778899aabbccdd\trefs/heads/v1\n"))
	assert.Empty(t, branch)
	assert.Len(t, refs, 1)
}

func TestParseLsRemoteRefsOfNothing(t *testing.T) {
	refs, branch := parseLsRemoteRefs(nil)
	assert.Empty(t, branch)
	assert.Empty(t, refs)
}

// WithQuiet() sends git's stderr nowhere, so a bare exit status is the whole
// report until gitOutput attaches what git said.
func TestGitOutputCarriesWhatGitSaid(t *testing.T) {
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		return runner.MockProcessWithStderr(nil, []byte("  fatal: repository not found  "), assert.AnError), nil
	}

	_, err := gitOutput(mock, "git", "ls-remote", "https://example.com/absent")
	require.Error(t, err)
	assert.ErrorIs(t, err, assert.AnError)
	assert.Contains(t, err.Error(), "fatal: repository not found")
}

// A failure git says nothing about reads as the exit status alone.
func TestGitOutputWithoutStderrIsTheBareError(t *testing.T) {
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		return runner.MockProcessWithStderr(nil, nil, assert.AnError), nil
	}

	_, err := gitOutput(mock, "git", "rev-parse", "HEAD")
	require.Error(t, err)
	assert.Equal(t, assert.AnError.Error(), err.Error())
}

func TestGitOutputReturnsStdout(t *testing.T) {
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		return runner.MockProcess([]byte("5bf638fdce71aa0d6a3b2a8bdb9a4a7f3c2d1e0f\n"), nil), nil
	}

	out, err := gitOutput(mock, "git", "rev-parse", "HEAD")
	require.NoError(t, err)
	assert.Equal(t, "5bf638fdce71aa0d6a3b2a8bdb9a4a7f3c2d1e0f\n", string(out))
}
