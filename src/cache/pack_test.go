package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// hexID returns a deterministic hex action/output ID from a seed byte.
func hexID(seed byte) string {
	b := make([]byte, hashLen)
	for i := range b {
		b[i] = seed + byte(i)
	}
	return strings.ToLower(hexEncode(b))
}

// casID returns body's content-addressed outputID (lowercase hex sha256), matching what the go command uses.
func casID(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func hexEncode(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hexdigits[c>>4]
		out[i*2+1] = hexdigits[c&0xf]
	}
	return string(out)
}

func TestPackStore_PutGetReadAll(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)
	defer s.Close()

	aid, oid := hexID(1), hexID(100)
	body := []byte("the quick brown fox")

	loc, err := s.Put(aid, oid, bytes.NewReader(body))
	require.Nil(t, err)
	require.Equal(t, int64(len(body)), loc.dataLen)
	require.Equal(t, oid, loc.outputID)

	got, ok := s.Get(aid)
	require.True(t, ok)
	require.Equal(t, oid, got.outputID)

	data, err := s.ReadAll(got)
	require.Nil(t, err)
	require.True(t, bytes.Equal(body, data))
}

func TestPackStore_Miss(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)
	defer s.Close()

	_, ok := s.Get(hexID(9))
	require.False(t, ok)
}

func TestPackStore_ReadAtPartial(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)
	defer s.Close()

	body := []byte("0123456789abcdef")
	loc, err := s.Put(hexID(1), hexID(2), bytes.NewReader(body))
	require.Nil(t, err)

	// Read a middle window, mimicking an mmap'd random read.
	buf := make([]byte, 5)
	n, err := s.ReadAt(loc, buf, 4)
	require.Nil(t, err)
	require.Equal(t, 5, n)
	require.Equal(t, "456789"[:5], string(buf[:n]))

	// Reading at/after EOF returns io.EOF and no bytes.
	_, err = s.ReadAt(loc, buf, int64(len(body)))
	require.NotNil(t, err)
}

func TestPackStore_PersistsAcrossReopen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)

	entries := map[string]string{}
	for i := 0; i < 20; i++ {
		aid, oid := hexID(byte(i)), hexID(byte(100+i))
		body := bytes.Repeat([]byte{byte('a' + i)}, i+1)
		_, err := s.Put(aid, oid, bytes.NewReader(body))
		require.Nil(t, err)
		entries[aid] = string(body)
	}
	require.Nil(t, s.Close())

	// Reopen: the index must be rebuilt purely from scanning the packs.
	s2, err := OpenPackStore(dir)
	require.Nil(t, err)
	defer s2.Close()
	require.Equal(t, len(entries), s2.Len())
	for aid, want := range entries {
		loc, ok := s2.Get(aid)
		require.True(t, ok)
		data, err := s2.ReadAll(loc)
		require.Nil(t, err)
		require.Equal(t, want, string(data))
	}
}

func TestPackStore_ContentDedup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)
	defer s.Close()

	oid := hexID(50)
	body := bytes.Repeat([]byte("x"), 1000)

	loc1, err := s.Put(hexID(1), oid, bytes.NewReader(body))
	require.Nil(t, err)
	loc2, err := s.Put(hexID(2), oid, bytes.NewReader(body))
	require.Nil(t, err)

	// Same content (outputID) => the repeat Put reuses the stored body's offset.
	require.Equal(t, loc1.dataOff, loc2.dataOff)

	// The body is stored a single time; the repeat Put adds only a header-only alias record.
	info, err := os.Stat(s.packPath(1))
	require.Nil(t, err)
	require.Equal(t, int64(packHeaderLen+len(body)+packHeaderLen), info.Size())

	// Both actions resolve to the same bytes.
	for _, aid := range []string{hexID(1), hexID(2)} {
		got, ok := s.Get(aid)
		require.True(t, ok)
		data, err := s.ReadAll(got)
		require.Nil(t, err)
		require.Equal(t, body, data)
	}
}

// TestPackStore_DedupPersistsAcrossReopen guards the warm-cache regression:
// when separate actions share content (the very common empty-output case), the
// later action is stored as an alias record. That alias MUST survive a reopen, or the
// next build misses it and falls through to the (slow) network tier.
func TestPackStore_DedupPersistsAcrossReopen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)

	empty := hexID(200) // outputID for empty content
	// All produce identical (empty) content — only the earliest writes a body; the rest are aliases.
	var actions []string
	for i := 0; i < 50; i++ {
		a := hexID(byte(i))
		actions = append(actions, a)
		_, err := s.Put(a, empty, bytes.NewReader(nil))
		require.Nil(t, err)
	}
	// A couple with non-empty shared content too.
	shared := hexID(201)
	body := []byte("shared body across actions")
	_, err = s.Put(hexID(100), shared, bytes.NewReader(body))
	require.Nil(t, err)
	_, err = s.Put(hexID(101), shared, bytes.NewReader(body))
	require.Nil(t, err)
	require.Nil(t, s.Close())

	// Reopen: every deduped action must still resolve from disk alone.
	s2, err := OpenPackStore(dir)
	require.Nil(t, err)
	defer s2.Close()
	for _, a := range actions {
		loc, ok := s2.Get(a)
		require.True(t, ok, "deduped action %s lost after reopen", a)
		require.Equal(t, empty, loc.outputID)
		require.Equal(t, int64(0), loc.dataLen)
	}
	for _, a := range []string{hexID(100), hexID(101)} {
		loc, ok := s2.Get(a)
		require.True(t, ok, "shared-content action %s lost after reopen", a)
		data, err := s2.ReadAll(loc)
		require.Nil(t, err)
		require.Equal(t, body, data)
	}
	// The shared non-empty body is stored a single time (a full record plus an alias), never duplicated.
	info, err := os.Stat(s2.packPath(1))
	require.Nil(t, err)
	// A header per empty action, plus the shared full record and its alias header.
	want := int64(50*packHeaderLen) + int64(packHeaderLen+len(body)) + int64(packHeaderLen)
	require.Equal(t, want, info.Size())
}

func TestPackStore_OverwriteAction(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)

	aid := hexID(1)
	_, err = s.Put(aid, hexID(10), bytes.NewReader([]byte("first")))
	require.Nil(t, err)
	_, err = s.Put(aid, hexID(20), bytes.NewReader([]byte("second")))
	require.Nil(t, err)

	got, ok := s.Get(aid)
	require.True(t, ok)
	require.Equal(t, hexID(20), got.outputID)
	data, err := s.ReadAll(got)
	require.Nil(t, err)
	require.Equal(t, "second", string(data))
	require.Nil(t, s.Close())

	// Last-write-wins must survive a reopen (scan order is file order).
	s2, err := OpenPackStore(dir)
	require.Nil(t, err)
	defer s2.Close()
	got2, ok := s2.Get(aid)
	require.True(t, ok)
	require.Equal(t, hexID(20), got2.outputID)
}

func TestPackStore_TornFinalRecordIgnored(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)
	_, err = s.Put(hexID(1), hexID(10), bytes.NewReader([]byte("good record")))
	require.Nil(t, err)
	require.Nil(t, s.Close())

	// A bogus record header claiming a body past EOF, as a crash mid-append leaves behind.
	f, err := os.OpenFile(s.packPath(1), os.O_WRONLY|os.O_APPEND, 0o644)
	require.Nil(t, err)
	torn := make([]byte, packHeaderLen)
	binary.LittleEndian.PutUint32(torn[0:4], packRecordMagic)
	binary.LittleEndian.PutUint64(torn[12+2*hashLen:20+2*hashLen], 1<<40) // absurd dataLen
	_, err = f.Write(torn)
	require.Nil(t, err)
	require.Nil(t, f.Close())

	// Reopen must recover the good record and silently drop the torn record.
	s2, err := OpenPackStore(dir)
	require.Nil(t, err)
	defer s2.Close()
	require.Equal(t, 1, s2.Len())
	loc, ok := s2.Get(hexID(1))
	require.True(t, ok)
	data, err := s2.ReadAll(loc)
	require.Nil(t, err)
	require.Equal(t, "good record", string(data))
}

// NOT parallel: it shrinks the package-global maxPackBytes, so a parallel
// sibling would rotate its own packs and stat a pack holding a fraction of
// its records.
func TestPackStore_Rotation(t *testing.T) {
	orig := maxPackBytes
	maxPackBytes = int64(packHeaderLen + 10) // rotate after ~every record
	defer func() { maxPackBytes = orig }()

	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)
	defer s.Close()

	for i := 0; i < 5; i++ {
		_, err := s.Put(hexID(byte(i)), hexID(byte(50+i)), bytes.NewReader([]byte("0123456789")))
		require.Nil(t, err)
	}
	// Several pack files should now exist.
	ids, _, err := s.discoverPacks()
	require.Nil(t, err)
	require.Greater(t, len(ids), 1)

	// And every entry is still retrievable across the rotated packs.
	for i := 0; i < 5; i++ {
		_, ok := s.Get(hexID(byte(i)))
		require.True(t, ok)
	}
}

// TestPackStore_EvictsOldestPacksWhenOverBudget replaces the old wholesale
// reset (which nuked EVERY pack the moment the total crossed the budget, so
// a working set >= budget cold-cycled forever): the oldest packs are evicted
// until the total is back under the eviction target, and the newest records
// survive.
// NOT parallel: it shrinks maxPackBytes and packResetBytes, which every other
// test in the package reads.
func TestPackStore_EvictsOldestPacksWhenOverBudget(t *testing.T) {
	origMax, origReset := maxPackBytes, packResetBytes
	maxPackBytes = int64(packHeaderLen + 512) // rotate after every record
	defer func() { maxPackBytes = origMax; packResetBytes = origReset }()

	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)

	// Records with DISTINCT content (dedup would alias them), a pack each.
	const n = 10
	recBytes := int64(packHeaderLen + 512)
	for i := 0; i < n; i++ {
		body := bytes.Repeat([]byte{byte('a' + i)}, 512)
		_, err := s.Put(hexID(byte(i+1)), casID(body), bytes.NewReader(body))
		require.Nil(t, err)
	}
	require.Nil(t, s.Close())

	// The written total sits over the budget, so the oldest packs must go until it is under the target.
	packResetBytes = 3000

	s2, err := OpenPackStore(dir)
	require.Nil(t, err)
	defer s2.Close()

	for i := 0; i < 6; i++ {
		_, ok := s2.Get(hexID(byte(i + 1)))
		require.False(t, ok, "oldest record %d must have been evicted", i+1)
	}
	for i := 6; i < n; i++ {
		loc, ok := s2.Get(hexID(byte(i + 1)))
		require.True(t, ok, "newest record %d must survive eviction", i+1)
		data, err := s2.ReadAll(loc)
		require.Nil(t, err)
		require.Equal(t, bytes.Repeat([]byte{byte('a' + i)}, 512), data)
	}

	_, total, err := s2.discoverPacks()
	require.Nil(t, err)
	require.LessOrEqual(t, total, packResetBytes/10*8+recBytes,
		"surviving packs must be back around the eviction target")
}

// TestPackStore_EvictionNeverDeletesNewestPack: even when a single pack alone
// exceeds the whole budget, the newest pack (the append target, holding the
// hottest records) is never deleted — the store runs over budget instead of
// cold-cycling.
// NOT parallel: it shrinks the package-global packResetBytes.
func TestPackStore_EvictionNeverDeletesNewestPack(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)
	body := bytes.Repeat([]byte("z"), 4096)
	_, err = s.Put(hexID(1), casID(body), bytes.NewReader(body))
	require.Nil(t, err)
	require.Nil(t, s.Close())

	orig := packResetBytes
	packResetBytes = 100
	defer func() { packResetBytes = orig }()

	s2, err := OpenPackStore(dir)
	require.Nil(t, err)
	defer s2.Close()
	require.Equal(t, 1, s2.Len(), "the newest pack must never be evicted")
	loc, ok := s2.Get(hexID(1))
	require.True(t, ok)
	data, err := s2.ReadAll(loc)
	require.Nil(t, err)
	require.Equal(t, body, data)
}

func TestParsePackName(t *testing.T) {
	t.Parallel()
	id, ok := parsePackName("pack-000007.data")
	require.True(t, ok)
	require.Equal(t, 7, id)

	_, ok = parsePackName("pack-.data")
	require.False(t, ok)
	_, ok = parsePackName("notapack.txt")
	require.False(t, ok)
	_, ok = parsePackName("pack-12ab.data")
	require.False(t, ok)
}

func TestDecodeHash(t *testing.T) {
	t.Parallel()
	_, err := decodeHash(hexID(1))
	require.Nil(t, err)
	_, err = decodeHash("tooshort")
	require.NotNil(t, err)
	_, err = decodeHash(strings.Repeat("z", 64))
	require.NotNil(t, err)
}

// packPath is exercised indirectly above; this guards the naming scheme.
func TestPackStore_PackPath(t *testing.T) {
	t.Parallel()
	s := &PackStore{dir: "/tmp/x"}
	require.Equal(t, filepath.Join("/tmp/x", "pack-000001.data"), s.packPath(1))
}
