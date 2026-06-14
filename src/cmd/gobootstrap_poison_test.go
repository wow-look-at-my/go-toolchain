package cmd

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestUnpoisonGoVersion(t *testing.T) {
	// A poisoned version is substituted for its known-good replacement.
	got, poisoned := unpoisonGoVersion("1.24.13")
	assert.False(t, !poisoned || got != "1.25.11")

	// Clean versions pass through unchanged (no false positives on adjacent patches).
	for _, v := range []string{"1.24.7", "1.24.12", "1.24.14", "1.25.0", "1.23.0"} {
		got, poisoned := unpoisonGoVersion(v)
		assert.False(t, poisoned || got != v)

	}
}
