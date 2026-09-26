package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
// suites answer for them.

// The sweep repairs the tree the caller names, and a join answers what it did.
func TestTheSweepRepairsTheTreeAndReportsIt(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module x\n")
	write(t, dir, "a.go", "package p\n\n// The walk holds 3 phases.\nfunc f() {}\n")

	scan := startRepair(dir)
	<-scan.done
	require.Len(t, scan.result.Repaired, 1)
	assert.Empty(t, scan.result.Findings, "slopfix repairs every finding it reports")

	src, err := os.ReadFile(filepath.Join(dir, "a.go"))
	require.NoError(t, err)
	assert.NotContains(t, string(src), "3 phases")
	assert.Contains(t, string(src), "func f() {}", "the repair stays inside the comment")
}

// The sweep takes every rule slopfix carries, not the comment ones alone. A
// document is outside what a comment repair can reach, so a rewrite here is
// what separates this from a sweep that only warns about prose.
func TestTheSweepRepairsProseOutsideComments(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module x\n")
	write(t, dir, "README.md", "# Title\n\nIt doesn't hold the lock, and it shouldn't.\n")

	scan := startRepair(dir)
	<-scan.done
	require.Len(t, scan.result.Repaired, 1, "the document was rewritten")

	body, err := os.ReadFile(filepath.Join(dir, "README.md"))
	require.NoError(t, err)
	assert.NotContains(t, string(body), "doesn't", "the contraction is expanded")
	assert.NotContains(t, string(body), "shouldn't")
	assert.Contains(t, string(body), "# Title", "the repair leaves the heading alone")
}

// The join is what the build waits on, and a run that started no sweep must
// not wait at all. Every caller reaching no module is in that case.
func TestJoiningWithoutASweepAnswersAtOnce(t *testing.T) {
	t.Serial()
	activeRepair = nil
	assert.NotPanics(t, waitForRepair)
}

// The join is spent a single time. The test phase and the deferred join in run
// both call it, and the next must not block on a channel already drained.
func TestJoiningTwiceIsSafe(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	write(t, dir, "go.mod", "module x\n")
	write(t, dir, "a.go", "package p\n")

	activeRepair = startRepair(dir)
	waitForRepair()
	assert.Nil(t, activeRepair)
	assert.NotPanics(t, waitForRepair)
}
