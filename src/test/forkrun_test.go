package test

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestForkRunNamesTheJobRun(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "wow-look-at-my/go-toolchain")
	t.Setenv("GITHUB_RUN_ID", "4242")
	t.Setenv("GITHUB_RUN_ATTEMPT", "2")
	assert.Equal(t, "wow-look-at-my/go-toolchain/4242/2", forkRun())
}

func TestForkRunIsEmptyOutsideARun(t *testing.T) {
	for _, blank := range []string{"GITHUB_REPOSITORY", "GITHUB_RUN_ID", "GITHUB_RUN_ATTEMPT"} {
		t.Setenv("GITHUB_REPOSITORY", "wow-look-at-my/go-toolchain")
		t.Setenv("GITHUB_RUN_ID", "4242")
		t.Setenv("GITHUB_RUN_ATTEMPT", "2")
		t.Setenv(blank, "")
		assert.Empty(t, forkRun(), "a run with %s blank names no run, so the go command reports the missing variable", blank)
	}
}
