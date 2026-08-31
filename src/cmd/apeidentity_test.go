package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTempFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, content, 0o644))
	return path
}

func TestParseNamedFiles(t *testing.T) {
	files, err := parseNamedFiles([]string{"linux=a", "darwin=b"})
	require.NoError(t, err)
	assert.Equal(t, []namedFile{{name: "linux", path: "a"}, {name: "darwin", path: "b"}}, files)

	_, err = parseNamedFiles([]string{"nope"})
	assert.Error(t, err)

	_, err = parseNamedFiles([]string{"=b"})
	assert.Error(t, err)
}

func TestRunVerifyIdentical_AllMatch(t *testing.T) {
	dir := t.TempDir()
	content := []byte("same bytes")
	a := writeTempFile(t, dir, "a", content)
	b := writeTempFile(t, dir, "b", content)
	c := writeTempFile(t, dir, "c", content)

	err := runVerifyIdentical(nil, []string{"linux=" + a, "darwin=" + b, "windows=" + c})
	assert.NoError(t, err)
}

func TestRunVerifyIdentical_ReportsEveryMismatch(t *testing.T) {
	// logger.Error routes to stdout as a ::error:: annotation under GITHUB_ACTIONS=true; pin non-GHA mode for stderr capture.
	t.Setenv("GITHUB_ACTIONS", "")
	dir := t.TempDir()
	a := writeTempFile(t, dir, "a", []byte("reference"))
	b := writeTempFile(t, dir, "b", []byte("different"))
	c := writeTempFile(t, dir, "c", []byte("also different"))

	var stderr string
	var err error
	stderr = captureStderr(t, func() {
		err = runVerifyIdentical(nil, []string{"linux=" + a, "darwin=" + b, "windows=" + c})
	})
	require.Error(t, err)
	assert.Contains(t, stderr, "the darwin build differs from the linux build")
	assert.Contains(t, stderr, "the windows build differs from the linux build")
}

func TestRunVerifyIdentical_MissingFileNamesTheHost(t *testing.T) {
	// logger.Error routes to stdout as a ::error:: annotation under GITHUB_ACTIONS=true; pin non-GHA mode for stderr capture.
	t.Setenv("GITHUB_ACTIONS", "")
	dir := t.TempDir()
	a := writeTempFile(t, dir, "a", []byte("x"))

	var stderr string
	var err error
	stderr = captureStderr(t, func() {
		err = runVerifyIdentical(nil, []string{"linux=" + a, "darwin=" + filepath.Join(dir, "missing")})
	})
	require.Error(t, err)
	assert.Contains(t, stderr, "darwin handed off no APE")
}
