package cosmocompat

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeLibFile writes name under moduleOut/lib with the given //go:build
// line (empty means rely on the filename convention alone, matching how
// modernc.org/undup emits a platform-specific override).
func writeLibFile(t *testing.T, moduleOut, name, buildTag, body string) {
	t.Helper()
	dir := filepath.Join(moduleOut, "lib")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	var content string
	if buildTag != "" {
		content = "//go:build " + buildTag + "\n\npackage lib\n\n" + body
	} else {
		content = "package lib\n\n" + body
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}

// TestDirMatchCopies_SiblingDefaultCoversCosmo reproduces modernc.org/sqlite
// v1.57.0's lib/hooks.go ("!(linux && arm64)") / lib/hooks_linux_arm64.go
// (filename-only) pair: hooks.go already declares X__ccgo_sqlite3_log under
// cosmo on its own, so hooks_linux_arm64.go -- whose own tag is
// filename-gated and can never match GOOS=cosmo -- must NOT also get a
// cosmo copy, or the two collide on the same symbol.
func TestDirMatchCopies_SiblingDefaultCoversCosmo(t *testing.T) {
	dir := t.TempDir()
	writeLibFile(t, dir, "hooks.go", "!(linux && arm64)", "func X__ccgo_sqlite3_log() {}\n")
	writeLibFile(t, dir, "hooks_linux_arm64.go", "", "func X__ccgo_sqlite3_log() {}\n")

	copies, err := dirMatchCopies(dir, dirMatch{dir: "lib", goos: "linux", goarch: "arm64", archTag: "arm64"})
	require.NoError(t, err)
	assert.Empty(t, copies, "hooks.go already covers cosmo/arm64; hooks_linux_arm64.go must not get a redundant copy")
}

// TestDirMatchCopies_GenuineGapStillCopied guards the fix above against
// over-suppressing: a filename-gated override with NO negated sibling is a
// real cosmo gap and must still get its cosmo-tagged copy.
func TestDirMatchCopies_GenuineGapStillCopied(t *testing.T) {
	dir := t.TempDir()
	writeLibFile(t, dir, "quirk_linux_arm64.go", "", "func Quirk() {}\n")

	copies, err := dirMatchCopies(dir, dirMatch{dir: "lib", goos: "linux", goarch: "arm64", archTag: "arm64"})
	require.NoError(t, err)
	require.Len(t, copies, 1)
	assert.Equal(t, filepath.Join("lib", "quirk_linux_arm64.go"), copies[0].src)
	assert.Equal(t, filepath.Join("lib", "quirk_linux_arm64_cosmo_arm64.go"), copies[0].dst)
	assert.Equal(t, "arm64", copies[0].extraCond)
}

// TestDirMatchCopies_SiblingPresentButNotCosmoInclusive covers a base file
// that exists but whose OWN tag excludes cosmo too (so it is not a
// negated "default" -- the override is still a genuine gap). The base
// file's tag targets an unrelated real platform, so it is neither a real
// linux/arm64 candidate itself nor cosmo-inclusive.
func TestDirMatchCopies_SiblingPresentButNotCosmoInclusive(t *testing.T) {
	dir := t.TempDir()
	writeLibFile(t, dir, "quirk.go", "windows", "func Quirk() {}\n")
	writeLibFile(t, dir, "quirk_linux_arm64.go", "", "func QuirkLinuxArm64() {}\n")

	copies, err := dirMatchCopies(dir, dirMatch{dir: "lib", goos: "linux", goarch: "arm64", archTag: "arm64"})
	require.NoError(t, err)
	require.Len(t, copies, 1)
	assert.Equal(t, filepath.Join("lib", "quirk_linux_arm64.go"), copies[0].src)
}
