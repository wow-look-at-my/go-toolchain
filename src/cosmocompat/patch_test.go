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

// TestAddCosmoFile_OptionalMissingSourceSkipsSilently reproduces the
// x/sys v0.42.0 case: readv_unix.go's helpers are still inline in
// syscall_linux.go at that version, so the file genuinely doesn't exist and
// addCosmoFile must skip it rather than fail the whole gap.
func TestAddCosmoFile_OptionalMissingSourceSkipsSilently(t *testing.T) {
	dir := t.TempDir()
	c := copySpec{src: "unix/readv_unix.go", dst: "unix/readv_cosmo.go"}

	require.NoError(t, addCosmoFile(dir, c))
	_, err := os.Stat(filepath.Join(dir, c.dst))
	assert.True(t, os.IsNotExist(err), "no destination file should be written when the optional source is missing")
}

// TestAddCosmoFile_NonOptionalMissingSourceStillFails guards the fix above
// against over-broadening: a copySpec not in optionalCopies must still hard
// -fail on a missing source, exactly as before.
func TestAddCosmoFile_NonOptionalMissingSourceStillFails(t *testing.T) {
	dir := t.TempDir()
	c := copySpec{src: "unix/does_not_exist.go", dst: "unix/does_not_exist_cosmo.go"}

	err := addCosmoFile(dir, c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does_not_exist.go")
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

// TestDirMatchCopies_UntaggedCommonFileIsNotMistakenForTheDefaultHalf
// reproduces modernc.org/sqlite v1.57.0's lib/sqlite.go / lib/sqlite_linux_amd64.go
// pair: sqlite.go carries NO build tag at all (it holds declarations common
// to every platform, from modernc.org/undup's split), so it also compiles
// for real linux/amd64 -- it is not sqlite_linux_amd64.go's negated
// complement, just a same-stemmed file by naming coincidence, and must not
// suppress the genuine cosmo gap sqlite_linux_amd64.go's platform-only
// symbols (like Tstat) leave behind.
func TestDirMatchCopies_UntaggedCommonFileIsNotMistakenForTheDefaultHalf(t *testing.T) {
	dir := t.TempDir()
	writeLibFile(t, dir, "sqlite.go", "", "const ALLBITS = -1\n")
	writeLibFile(t, dir, "sqlite_linux_amd64.go", "", "type Tstat = struct{}\n")

	copies, err := dirMatchCopies(dir, dirMatch{dir: "lib", goos: "linux", goarch: "amd64", archTag: "amd64"})
	require.NoError(t, err)
	// sqlite.go itself needs no copy -- untagged, it already compiles under
	// cosmo directly. sqlite_linux_amd64.go's Tstat is a genuine cosmo gap
	// and must still get one, despite sharing sqlite.go's stem.
	require.Len(t, copies, 1)
	assert.Equal(t, filepath.Join("lib", "sqlite_linux_amd64.go"), copies[0].src)
	assert.Equal(t, filepath.Join("lib", "sqlite_linux_amd64_cosmo_amd64.go"), copies[0].dst)
}
