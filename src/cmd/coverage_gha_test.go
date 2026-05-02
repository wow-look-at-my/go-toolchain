package cmd

import (
	"os"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

// TestCoverageBelowMinimum_GHAErrorAnnotation verifies that a coverage shortfall
// emits a ::error:: annotation when running inside GitHub Actions, so the
// failure shows up as a tagged error in the workflow UI.
func TestCoverageBelowMinimum_GHAErrorAnnotation(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject()
	t.Setenv("GITHUB_ACTIONS", "true")
	jsonOutput = false
	defer func() { jsonOutput = false }()

	// 60 covered / 40 uncovered = 60% (well below 80%, with >=10 uncovered)
	mock := newSmallMock(60, 40)

	var err error
	output := captureStdout(func() {
		err = runWithRunner(mock, nil)
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "below minimum")
	assert.Contains(t, output, "::error ::coverage ")
	assert.Contains(t, output, "is below minimum")
}

// TestCoverageBelowMinimum_NoGHAAnnotationLocally verifies that no GHA annotation
// is emitted when not running outside GitHub Actions (avoids duplicating the
// error message that cobra already prints).
func TestCoverageBelowMinimum_NoGHAAnnotationLocally(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject()
	t.Setenv("GITHUB_ACTIONS", "")
	jsonOutput = false
	defer func() { jsonOutput = false }()

	mock := newSmallMock(60, 40)

	var err error
	output := captureStdout(func() {
		err = runWithRunner(mock, nil)
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "below minimum")
	assert.NotContains(t, output, "::error")
}
