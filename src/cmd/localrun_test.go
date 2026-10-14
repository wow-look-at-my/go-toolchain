package cmd

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalRunNameHasTheShapeTheForkAccepts(t *testing.T) {
	t.Serial()
	parts := strings.Split(localRunName(1234, time.Unix(0, 42)), "/")
	require.Len(t, parts, 4)
	for _, part := range parts {
		assert.NotEmpty(t, part)
	}
}

// A local build gets a run, so its go commands share one resolution of each head.
func TestNameLocalRunNamesALocalBuild(t *testing.T) {
	t.Serial()
	t.Setenv("GITHUB_RUN_ID", "")
	t.Setenv(gosmoRunEnv, "")
	nameLocalRun()
	assert.NotEmpty(t, os.Getenv(gosmoRunEnv))
}

// A CI job locks its heads under its own run, and a name the caller set wins.
func TestNameLocalRunLeavesAnExistingRun(t *testing.T) {
	t.Serial()
	t.Setenv("GITHUB_RUN_ID", "77")
	t.Setenv(gosmoRunEnv, "")
	nameLocalRun()
	assert.Empty(t, os.Getenv(gosmoRunEnv))

	t.Setenv("GITHUB_RUN_ID", "")
	t.Setenv(gosmoRunEnv, "me/repo/1/1")
	nameLocalRun()
	assert.Equal(t, "me/repo/1/1", os.Getenv(gosmoRunEnv))
}
