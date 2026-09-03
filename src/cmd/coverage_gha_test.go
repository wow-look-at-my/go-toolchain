package cmd

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCoverageBelowMinimum_GHAErrorAnnotation verifies that a coverage shortfall
// emits a GitHub Actions error workflow command when running inside GitHub
// Actions, so the failure shows up as a tagged error in the workflow UI.
func TestCoverageBelowMinimum_GHAErrorAnnotation(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject(t)
	t.Setenv("GITHUB_ACTIONS", "true")
	jsonOutput = false
	defer func() { jsonOutput = false }()

	// well below the minimum, with too many uncovered statements to excuse
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
// is emitted when not running inside GitHub Actions (avoids duplicating the
// error message that cobra already prints).
func TestCoverageBelowMinimum_NoGHAAnnotationLocally(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject(t)
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

// TestCoverageBelowMinimum_NoGHAAnnotationInJSONMode verifies that no GHA
// annotation is emitted in --json mode even when running inside GitHub Actions:
// stdout is reserved for the JSON payload and a workflow command would corrupt
// it for programmatic consumers.
func TestCoverageBelowMinimum_NoGHAAnnotationInJSONMode(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject(t)
	t.Setenv("GITHUB_ACTIONS", "true")
	jsonOutput = true
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
