package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestJoinLDFlagsLetsAnExplicitValueOverrideTheStamp(t *testing.T) {
	t.Parallel()
	// The linker keeps the LAST -X given for a name, so the caller's spelling
	// has to trail the stamp for an explicit revision to win.
	assert.Equal(t, "-X m.gitHash=stamped -X m.gitHash=chosen",
		joinLDFlags("-X m.gitHash=stamped", "-X m.gitHash=chosen"))
	assert.Equal(t, "-X m.gitHash=stamped", joinLDFlags("-X m.gitHash=stamped", ""))
	assert.Equal(t, "-s -w", joinLDFlags("", "-s -w"))
	assert.Equal(t, "", joinLDFlags("", ""))
}
