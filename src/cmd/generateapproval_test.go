package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// write puts an approval file in a fresh directory and answers the directory.
func withApproval(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, generateApprovalFile), []byte(body), 0o644))
	return dir
}

// A repo that records nothing approves nothing, which is the state every repo
// starts in.
func TestNoApprovalFileApprovesNothing(t *testing.T) {
	assert.Empty(t, readGenerateApproval(t.TempDir()))
}

// The recorded hash is what a bare run may execute, so a fresh clone needs no
// argument to build a repo whose output a directive writes.
func TestTheRecordedHashIsRead(t *testing.T) {
	assert.Equal(t, "fa5070b62ebc", readGenerateApproval(withApproval(t, "fa5070b62ebc\n")))
}

// Surrounding whitespace is not part of a hash.
func TestSurroundingWhitespaceIsNotPartOfTheHash(t *testing.T) {
	assert.Equal(t, "abc123", readGenerateApproval(withApproval(t, "\n  abc123  \n\n")))
}

// A note above the hash says why the repo approved it, which is the whole point
// of recording consent somewhere a reader sees it.
func TestANoteAboveTheHashIsNotTheHash(t *testing.T) {
	body := "# slopfix ships the parse-table directives and not their output.\nabc123\n"
	assert.Equal(t, "abc123", readGenerateApproval(withApproval(t, body)))
}

// A file holding only notes records no approval.
func TestAFileOfOnlyNotesApprovesNothing(t *testing.T) {
	assert.Empty(t, readGenerateApproval(withApproval(t, "# nothing approved yet\n")))
}

// The flag is for a one-off run, so it wins over what the tree records.
func TestTheFlagWinsOverTheRecordedHash(t *testing.T) {
	t.Serial()
	prev := generateHash
	generateHash = "fromflag"
	t.Cleanup(func() { generateHash = prev })
	t.Chdir(withApproval(t, "fromfile\n"))

	assert.Equal(t, "fromflag", approvedGenerateHash())
}

// Without the flag the tree's own record is the answer.
func TestTheTreeAnswersWhenNoFlagIsGiven(t *testing.T) {
	t.Serial()
	prev := generateHash
	generateHash = ""
	t.Cleanup(func() { generateHash = prev })
	t.Chdir(withApproval(t, "fromfile\n"))

	assert.Equal(t, "fromfile", approvedGenerateHash())
}
