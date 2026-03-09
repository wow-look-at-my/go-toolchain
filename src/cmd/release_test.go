package cmd

import (
	"strings"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
)

func TestGenerateReleaseNotes(t *testing.T) {
	commits := []string{"add feature X", "fix bug Y"}
	checksums := "abc123  tool_linux_amd64\ndef456  tool_darwin_arm64\n"

	notes := generateReleaseNotes("v1.0.0", commits, checksums)

	assert.Contains(t, notes, "## What's Changed")
	assert.Contains(t, notes, "- add feature X")
	assert.Contains(t, notes, "- fix bug Y")
	assert.Contains(t, notes, "## Checksums")
	assert.Contains(t, notes, "abc123  tool_linux_amd64")
	assert.Contains(t, notes, "## Verification")
	assert.Contains(t, notes, "cosign verify-blob")
}

func TestGenerateReleaseNotesNoChecksums(t *testing.T) {
	commits := []string{"initial commit"}
	notes := generateReleaseNotes("v0.1.0", commits, "")

	assert.Contains(t, notes, "## What's Changed")
	assert.Contains(t, notes, "- initial commit")
	assert.NotContains(t, notes, "## Checksums")
	assert.Contains(t, notes, "## Verification")
}

func TestGenerateReleaseNotesNoCommits(t *testing.T) {
	notes := generateReleaseNotes("v0.0.1", nil, "")

	assert.Contains(t, notes, "- Initial release")
}

func TestReleaseCmdAbort(t *testing.T) {
	t.Setenv("CI", "")
	stdin := strings.NewReader("n\n")
	err := runReleaseCmdWithStdin(stdin)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "release aborted")
}

func TestReleaseCmdAbortEmpty(t *testing.T) {
	t.Setenv("CI", "")
	stdin := strings.NewReader("\n")
	err := runReleaseCmdWithStdin(stdin)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "release aborted")
}
