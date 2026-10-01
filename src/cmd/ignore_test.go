package cmd

import (
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gotest "github.com/wow-look-at-my/go-toolchain/src/test"
)

func TestIgnoreCoverageBlockedOnCI(t *testing.T) {
	t.Serial()
	t.Setenv("CI", "true")
	err := runIgnoreCoverage(nil, nil)
	require.NotNil(t, err)
	assert.Contains(t, err.Error(), "can't use ignore coverage on CI")
}

func TestIgnoreCoverageBlockedOnClaudeCodeRemote(t *testing.T) {
	t.Serial()
	t.Setenv("CLAUDE_CODE_REMOTE", "true")
	err := runIgnoreCoverage(nil, nil)
	require.NotNil(t, err)
	assert.Contains(t, err.Error(), "can't use ignore coverage on CI")
}

func TestIgnoreCoverage(t *testing.T) {
	t.Serial()
	t.Setenv("CI", "")
	t.Setenv("CLAUDE_CODE_REMOTE", "")
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	err := runIgnoreCoverage(nil, nil)
	assert.Nil(t, err)

	wm, exists, werr := gotest.GetWatermark(".")
	require.Nil(t, werr)
	require.True(t, exists)
	assert.Equal(t, float32(0), wm)
}

func TestIgnoreCoverageAlreadyExists(t *testing.T) {
	t.Serial()
	t.Setenv("CI", "")
	t.Setenv("CLAUDE_CODE_REMOTE", "")
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	gotest.SetWatermark(".", 85.0)
	err := runIgnoreCoverage(nil, nil)
	assert.Nil(t, err)
	// Verify watermark was NOT changed
	wm, exists, _ := gotest.GetWatermark(".")
	assert.True(t, exists)
	assert.Equal(t, float32(85.0), wm)
}

func TestUnignoreCoverage(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	gotest.SetWatermark(".", 90.0)
	err := runUnignoreCoverage(nil, nil)
	assert.Nil(t, err)
	_, exists, _ := gotest.GetWatermark(".")
	assert.False(t, exists)
}

func TestUnignoreCoverageNoWatermark(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	err := runUnignoreCoverage(nil, nil)
	assert.Nil(t, err)
}

func TestUnignoreConfirmationAbort(t *testing.T) {
	t.Serial()
	oldStdin := os.Stdin
	rIn, wIn, _ := os.Pipe()
	wIn.WriteString("n\n")
	wIn.Close()
	os.Stdin = rIn
	defer func() { os.Stdin = oldStdin }()
	err := confirmUnignore()
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "aborted")
}

func TestUnignoreConfirmationAccept(t *testing.T) {
	t.Serial()
	oldStdin := os.Stdin
	rIn, wIn, _ := os.Pipe()
	wIn.WriteString("y\n")
	wIn.Close()
	os.Stdin = rIn
	defer func() { os.Stdin = oldStdin }()
	err := confirmUnignore()
	assert.Nil(t, err)
}

func TestUnignoreCoverageNoWatermarkMessage(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runUnignoreCoverage(nil, nil)

	w.Close()
	out, _ := io.ReadAll(r)
	os.Stdout = old

	assert.Nil(t, err)
	assert.Contains(t, string(out), "No watermark is set")
}
