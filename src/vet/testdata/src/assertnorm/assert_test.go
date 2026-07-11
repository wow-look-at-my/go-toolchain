package assertnorm

import (
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestNegatedTrue(t *testing.T) {
	ok := true

	assert.True(t, !ok) // want "use assert.False instead of assert.True with negation"
}

func TestNegatedFalse(t *testing.T) {
	ok := false

	assert.False(t, !ok) // want "use assert.True instead of assert.False with negation"
}

func TestNegatedRequireTrue(t *testing.T) {
	ok := true

	require.True(t, !ok) // want "use require.False instead of require.True with negation"
}

func TestNegatedRequireFalse(t *testing.T) {
	ok := false

	require.False(t, !ok) // want "use require.True instead of require.False with negation"
}

func TestWithMessage(t *testing.T) {
	ok := true

	assert.True(t, !ok, "should be false") // want "use assert.False instead of assert.True with negation"
}

func TestNoNegation(t *testing.T) {
	ok := true

	// These should NOT trigger
	assert.True(t, ok)
	assert.False(t, ok)
}
