package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCoverageDirIsAbsoluteAndExists(t *testing.T) {
	// A relative TMPDIR is what an NT host under a posix shell hands the fork.
	base := t.TempDir()
	t.Chdir(base)
	t.Setenv("TMPDIR", "reltmp")

	dir, err := coverageDir()
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(dir), "coverage dir must be absolute, got %s", dir)

	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}
