package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestWithGitStderr(t *testing.T) {
	assert.NoError(t, withGitStderr(nil, []byte("noise")))

	err := withGitStderr(assert.AnError, []byte("  fatal: repository not found  "))
	assert.ErrorIs(t, err, assert.AnError)
	assert.Contains(t, err.Error(), "fatal: repository not found")

	bare := withGitStderr(assert.AnError, nil)
	assert.Equal(t, assert.AnError.Error(), bare.Error())
}
