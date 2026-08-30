package cache

import (
	"bytes"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalCache_PutAndGet(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.Nil(t, err)

	actionID := "aabbccdd00112233aabbccdd00112233"
	body := []byte("hello, cache")
	outputID := casID(body) // Get verifies sha256(body) == outputID

	diskPath, err := lc.Put(actionID, outputID, bytes.NewReader(body))
	require.Nil(t, err)

	require.NotEqual(t, "", diskPath)

	// Verify file content.
	got, err := os.ReadFile(diskPath)
	require.Nil(t, err)

	require.True(t, bytes.Equal(got, body))

	// Get should return the cached entry.
	meta, miss := lc.Get(actionID)
	require.False(t, miss)

	require.Equal(t, outputID, meta.OutputID)

	require.Equal(t, diskPath, meta.DiskPath)

	require.Equal(t, int64(len(body)), meta.Size)

	// A repeat Get takes the memoized fast path and must serve identically.
	meta2, miss := lc.Get(actionID)
	require.False(t, miss)
	require.Equal(t, meta, meta2)
}

func TestLocalCache_Miss(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.Nil(t, err)

	_, miss := lc.Get("deadbeefdeadbeefdeadbeefdeadbeef")
	require.True(t, miss)

}

func TestLocalCache_Overwrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.Nil(t, err)

	actionID := "aabbccdd00112233aabbccdd00112233"
	second := []byte("second")

	_, err = lc.Put(actionID, casID([]byte("first")), bytes.NewReader([]byte("first")))
	require.Nil(t, err)

	// Memoize the entry, then overwrite: Put must invalidate the memo so new content is re-verified and served.
	_, miss := lc.Get(actionID)
	require.False(t, miss)

	_, err = lc.Put(actionID, casID(second), bytes.NewReader(second))
	require.Nil(t, err)

	meta, miss := lc.Get(actionID)
	require.False(t, miss)

	require.Equal(t, casID(second), meta.OutputID)

}

// TestLocalCache_RefusesChecksumMismatch covers the loose-tier integrity gap:
// a body that does not hash to its sidecar outputID (truncation, rot, or the
// empty body the old oversized-PUT bug stored) must be refused and evicted,
// never served. This tier used to serve bodies with NO read-side checks.
func TestLocalCache_RefusesChecksumMismatch(t *testing.T) {
	t.Parallel()
	lc, err := NewLocalCache(t.TempDir())
	require.Nil(t, err)

	actionID := "0102030401020304010203040102030401020304010203040102030401020304"
	body := []byte("the body that was supposed to be stored")
	// Sidecar advertises the correct body's hash, but an empty body (the exact poison the old PUT bug wrote) is on disk.
	diskPath, err := lc.Put(actionID, casID(body), bytes.NewReader(nil))
	require.Nil(t, err)

	_, miss := lc.Get(actionID)
	require.True(t, miss, "a body failing its content address must miss, never serve")
	require.Equal(t, uint32(1), lc.Stats.Corrupt.Load())
	_, err = os.Stat(diskPath)
	require.True(t, os.IsNotExist(err), "the corrupt entry must be evicted from disk")
	_, err = os.Stat(diskPath + ".meta")
	require.True(t, os.IsNotExist(err), "the sidecar must be evicted too")

	// A clean re-Put self-heals.
	_, err = lc.Put(actionID, casID(body), bytes.NewReader(body))
	require.Nil(t, err)
	meta, miss := lc.Get(actionID)
	require.False(t, miss)
	require.Equal(t, int64(len(body)), meta.Size)
}

// TestLocalCache_RefusesTruncatedBody: truncation after storage must be caught
// by the content-address check — the old code overwrote m.Size with the stat
// size, making truncation invisible.
func TestLocalCache_RefusesTruncatedBody(t *testing.T) {
	t.Parallel()
	lc, err := NewLocalCache(t.TempDir())
	require.Nil(t, err)

	actionID := "a1a2a3a4a1a2a3a4a1a2a3a4a1a2a3a4a1a2a3a4a1a2a3a4a1a2a3a4a1a2a3a4"
	body := []byte("a body that will be truncated behind the cache's back")
	diskPath, err := lc.Put(actionID, casID(body), bytes.NewReader(body))
	require.Nil(t, err)

	require.NoError(t, os.WriteFile(diskPath, body[:10], 0o644))

	_, miss := lc.Get(actionID)
	require.True(t, miss, "a truncated body must be refused")
	require.Equal(t, uint32(1), lc.Stats.Corrupt.Load())
}

// TestLocalCache_RefusesCrossContaminatedPackage: a compiled package whose
// build id belongs to a DIFFERENT action must be refused even though its
// bytes are self-consistent with its own content address.
func TestLocalCache_RefusesCrossContaminatedPackage(t *testing.T) {
	t.Parallel()
	lc, err := NewLocalCache(t.TempDir())
	require.Nil(t, err)

	actionA, actionB := hexID(20), hexID(99)
	poison := archiveWithBuildID(expectedBuildIDAction(actionB)) // stamped for B
	_, err = lc.Put(actionA, casID(poison), bytes.NewReader(poison))
	require.Nil(t, err)

	_, miss := lc.Get(actionA)
	require.True(t, miss, "a package stamped for another action must not be served")
	require.Equal(t, uint32(1), lc.Stats.Corrupt.Load())

	// A correctly-stamped package for the same action IS served.
	good := archiveWithBuildID(expectedBuildIDAction(actionA))
	_, err = lc.Put(actionA, casID(good), bytes.NewReader(good))
	require.Nil(t, err)
	_, miss = lc.Get(actionA)
	require.False(t, miss, "a correctly-keyed package must still be served")
}

// TestLocalCache_RefusesStoredModuleIndex: the loose tier never serves a Go
// module index, whatever put it there. An index key hashes no content, so no
// gate can tell a right body from a wrong body under it, and a wrong body renames
// a package at load time. Residue from a binary that predates the PUT refusal
// is evicted on the way out, so the toolchain recomputes it.
func TestLocalCache_RefusesStoredModuleIndex(t *testing.T) {
	t.Parallel()
	lc, err := NewLocalCache(t.TempDir())
	require.Nil(t, err)

	actionID := hexID(40)
	index := []byte("go index v2\n\x00\x00module-index payload from an older binary")
	diskPath, err := lc.Put(actionID, casID(index), bytes.NewReader(index))
	require.Nil(t, err)

	_, miss := lc.Get(actionID)
	require.True(t, miss, "a stored module index must never be served from the loose tier")

	// Evicted: data and sidecar are gone, so the residue cannot be re-offered.
	_, err = os.Stat(diskPath)
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(diskPath + ".meta")
	require.True(t, os.IsNotExist(err))
}

func TestLocalCache_SubdirCreation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := NewLocalCache(dir)
	require.Nil(t, err)

	// Verify a subdir exists per hex byte value.
	entries, _ := os.ReadDir(dir)
	require.Equal(t, 256, len(entries))

}

func TestLocalCache_DataPath(t *testing.T) {
	t.Parallel()
	lc := &LocalCache{dir: "/tmp/test-cache"}
	path := lc.dataPath("aabbccdd")
	expected := filepath.Join("/tmp/test-cache", "aa", "v1aabbccdd")
	require.Equal(t, expected, path)

}

func TestParseMeta(t *testing.T) {
	t.Parallel()
	now := time.Now().Unix()
	raw := "outputID:deadbeef\nsize:42\ntime:" + itoa(now) + "\n"
	m, err := parseMeta(raw)
	require.Nil(t, err)

	require.Equal(t, "deadbeef", m.OutputID)

	require.Equal(t, int64(42), m.Size)

	require.Equal(t, now, m.Time.Unix())

}

func TestParseMeta_MissingOutputID(t *testing.T) {
	t.Parallel()
	_, err := parseMeta("size:42\ntime:123\n")
	require.NotNil(t, err)

}

func TestParseMeta_InvalidSize(t *testing.T) {
	t.Parallel()
	_, err := parseMeta("outputID:abc\nsize:not-a-number\n")
	require.NotNil(t, err)

}

func TestParseMeta_InvalidTime(t *testing.T) {
	t.Parallel()
	_, err := parseMeta("outputID:abc\ntime:not-a-number\n")
	require.NotNil(t, err)

}

func itoa(n int64) string {
	return string([]byte{
		byte('0' + (n/1000000000)%10),
		byte('0' + (n/100000000)%10),
		byte('0' + (n/10000000)%10),
		byte('0' + (n/1000000)%10),
		byte('0' + (n/100000)%10),
		byte('0' + (n/10000)%10),
		byte('0' + (n/1000)%10),
		byte('0' + (n/100)%10),
		byte('0' + (n/10)%10),
		byte('0' + n%10),
	})
}
