package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// write puts a file at path under dir, creating the directories above it.
func write(t *testing.T, dir, path, body string) {
	t.Helper()
	full := filepath.Join(dir, path)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
}

// What this file owns is the WIRING. Which files carry prose, what a number in
// a comment is, and what a repair writes instead are slopfix's, and its own
// suites. Answer for them.

// The sweep repairs the tree the caller names, and a join answers what it did.
func TestTheSweepRepairsTheTreeAndReportsIt(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module x\n")
	write(t, dir, "a.go", "package p\n\n// The walk holds 3 phases.\nfunc f() {}\n")

	scan := startCommentScan(dir)
	<-scan.done
	require.Len(t, scan.result.Repaired, 1)
	assert.Empty(t, scan.result.Findings, "slopfix repairs every finding it reports")

	src, err := os.ReadFile(filepath.Join(dir, "a.go"))
	require.NoError(t, err)
	assert.NotContains(t, string(src), "3 phases")
	assert.Contains(t, string(src), "func f() {}", "the repair stays inside the comment")
}

// A file with no header spends one counted warning, although the branch did not change it.
func TestAFileWithNoHeaderSpendsOneWarning(t *testing.T) {
	t.Serial()
	logger.ResetWarnCount()
	defer logger.ResetWarnCount()
	dir := t.TempDir()
	write(t, dir, "go.mod", "module x\n")
	write(t, dir, "a.go", "package p\n\n// Load reads the settings.\nfunc Load() {}\n")
	gitIn(t, dir, "init", "-q", "-b", "master")
	gitIn(t, dir, "-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false", "add", "-A")
	gitIn(t, dir, "-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "base")

	scan := startCommentScan(dir)
	<-scan.done
	scan.report()

	assert.Equal(t, int64(1), logger.WarnCount())
	warnings := logger.EmittedWarnings()
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0].Message, "a.go:1: comments/header")
	assert.Contains(t, warnings[0].Message, "`slopfix fix a.go` writes the header")
	src, err := os.ReadFile(filepath.Join(dir, "a.go"))
	require.NoError(t, err)
	assert.Equal(t, "package p\n\n// Load reads the settings.\nfunc Load() {}\n", string(src), "the sweep writes only what the branch changed")
}

// The join is what the build waits on, and a run that started no sweep must
// not wait at all. Every caller reaching no module is in that case.
func TestJoiningWithoutASweepAnswersAtOnce(t *testing.T) {
	t.Serial()
	activeCommentScan = nil
	assert.NotPanics(t, waitForCommentScan)
}

// The join is spent a single time. The test phase and the deferred join in run
// both call it, and the next must not block on a channel already drained.
func TestJoiningTwiceIsSafe(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	write(t, dir, "go.mod", "module x\n")
	write(t, dir, "a.go", "package p\n")

	activeCommentScan = startCommentScan(dir)
	waitForCommentScan()
	assert.Nil(t, activeCommentScan)
	assert.NotPanics(t, waitForCommentScan)
}
