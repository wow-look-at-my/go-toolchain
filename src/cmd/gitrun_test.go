package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheAnswerNamesEveryRefAndTheDefaultBranch covers what the fork checkout
// reads: a ref's commit by name, and the branch behind a symbolic HEAD.
func TestTheAnswerNamesEveryRefAndTheDefaultBranch(t *testing.T) {
	out := []byte("ref: refs/heads/master\tHEAD\n" +
		"aaaaaaa\tHEAD\n" +
		"bbbbbbb\trefs/heads/master\n" +
		"ccccccc\trefs/heads/claude/work\n")

	refs, branch := parseLsRemoteRefs(out)

	assert.Equal(t, "master", branch)
	assert.Equal(t, "aaaaaaa", refs["HEAD"])
	assert.Equal(t, "bbbbbbb", refs["refs/heads/master"])
	assert.Equal(t, "ccccccc", refs["refs/heads/claude/work"])
}

// TestAnEmptyAnswerNamesNothing pins the reply from a reachable remote that
// has no such ref. It is not a failure, and it must not invent a ref.
func TestAnEmptyAnswerNamesNothing(t *testing.T) {
	refs, branch := parseLsRemoteRefs(nil)

	require.NotNil(t, refs)
	assert.Empty(t, refs)
	assert.Empty(t, branch)
}

// TestARefWithoutACommitIsSkipped guards the ragged line, which has no ref to key on.
func TestARefWithoutACommitIsSkipped(t *testing.T) {
	refs, _ := parseLsRemoteRefs([]byte("\n   \nddddddd\n"))

	assert.Empty(t, refs)
}
