package cache

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// hexID returns a deterministic 64-char hex action/output ID from a seed byte.
func hexID(seed byte) string {
	b := make([]byte, hashLen)
	for i := range b {
		b[i] = seed + byte(i)
	}
	return strings.ToLower(hexEncode(b))
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

func TestPackStore_GetVerifiedDetectsCorruptBody(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)
	defer s.Close()

	aid, oid := hexID(7), hexID(70)
	body := []byte("a module-index body that must never be served if corrupted")
	loc, err := s.Put(aid, oid, bytes.NewReader(body))
	require.Nil(t, err)

	// A clean body verifies and is served as a hit.
	got, ok := s.GetVerified(aid)
	require.True(t, ok)
	require.Equal(t, oid, got.outputID)

	// Corrupt one body byte in place: length and header (and thus the recorded
	// CRC) are untouched, so the scan still indexes it — exactly the case the
	// torn-tail check cannot catch (disk/overlay rot, or a partial overwrite).
	f := s.pack(loc.packID)
	require.NotNil(t, f)
	var b [1]byte
	_, err = f.ReadAt(b[:], loc.dataOff)
	require.Nil(t, err)
	_, err = f.WriteAt([]byte{b[0] ^ 0xff}, loc.dataOff)
	require.Nil(t, err)

	// The corrupt body must NOT be served: report a miss, bump the corruption
	// counter, and evict from both the action and output indexes.
	_, ok = s.GetVerified(aid)
	require.False(t, ok, "corrupt body must not be served as a hit")
	require.Equal(t, uint32(1), s.Stats.Corrupt.Load())
	_, ok = s.GetVerified(aid)
	require.False(t, ok, "evicted entry stays a miss")
	_, ok = s.GetByOutput(oid)
	require.False(t, ok, "corrupt body evicted from the output index too")

	// A fresh Put for the same action heals the cache.
	_, err = s.Put(aid, oid, bytes.NewReader(body))
	require.Nil(t, err)
	got, ok = s.GetVerified(aid)
	require.True(t, ok, "re-Put clean body is served again")
	data, err := s.ReadAll(got)
	require.Nil(t, err)
	require.True(t, bytes.Equal(body, data))
}

func TestPackStore_GetVerifiedDetectsCorruptLargeBody(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)
	defer s.Close()

	aid, oid := hexID(9), hexID(90)
	// Larger than mmapVerifyThreshold so verification takes the mmap path (a
	// page-aligned region map indexed into), not the small-body read.
	body := bytes.Repeat([]byte("go-toolchain pack-cache integrity probe; "), 4096) // ~168 KiB
	require.Greater(t, len(body), mmapVerifyThreshold)
	loc, err := s.Put(aid, oid, bytes.NewReader(body))
	require.Nil(t, err)

	// Clean large body verifies via mmap.
	_, ok := s.GetVerified(aid)
	require.True(t, ok)

	// Corrupt the final body byte: it sits well past the first page, so the
	// page-alignment/offset math and the full mapped span are both exercised.
	f := s.pack(loc.packID)
	require.NotNil(t, f)
	pos := loc.dataOff + loc.dataLen - 1
	var b [1]byte
	_, err = f.ReadAt(b[:], pos)
	require.Nil(t, err)
	_, err = f.WriteAt([]byte{b[0] ^ 0xff}, pos)
	require.Nil(t, err)

	_, ok = s.GetVerified(aid)
	require.False(t, ok, "corrupt large body must not be served (mmap verify path)")
	require.Equal(t, uint32(1), s.Stats.Corrupt.Load())
}

func TestPackStore_GetByOutputVerifiedDetectsCorruptBody(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)
	defer s.Close()

	aid, oid := hexID(11), hexID(110)
	body := []byte("a body resolved by outputID on the compiler's serve path")
	loc, err := s.Put(aid, oid, bytes.NewReader(body))
	require.Nil(t, err)

	// A clean body resolves by output and verifies.
	got, ok := s.GetByOutputVerified(oid)
	require.True(t, ok)
	require.Equal(t, oid, got.outputID)

	// Rot one body byte in place: header length + recorded CRC stay intact, so
	// the startup scan still indexes it (the disk/overlay-rot case the torn-tail
	// check cannot catch).
	f := s.pack(loc.packID)
	require.NotNil(t, f)
	var b [1]byte
	_, err = f.ReadAt(b[:], loc.dataOff)
	require.Nil(t, err)
	_, err = f.WriteAt([]byte{b[0] ^ 0xff}, loc.dataOff)
	require.Nil(t, err)

	// The unverified accessor (what Lookup used to call) cannot detect the rot —
	// it would hand these bytes to the compiler. This is the gap.
	_, stillThere := s.GetByOutput(oid)
	require.True(t, stillThere, "unverified GetByOutput cannot detect in-place rot")

	// The verified accessor refuses it: report a miss, bump the corruption
	// counter, and evict from the output index so the mount stops serving it.
	_, ok = s.GetByOutputVerified(oid)
	require.False(t, ok, "corrupt body must not be served on the verified serve path")
	require.Equal(t, uint32(1), s.Stats.Corrupt.Load())
	_, ok = s.GetByOutput(oid)
	require.False(t, ok, "corrupt body evicted from the output index")
}

func TestPackStore_Miss(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)
	defer s.Close()

	_, ok := s.Get(hexID(9))
	require.False(t, ok)
}

func TestPackStore_ReadAtPartial(t *testing.T) {
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

	// Reading at/after EOF returns io.EOF and zero bytes.
	_, err = s.ReadAt(loc, buf, int64(len(body)))
	require.NotNil(t, err)
}

func TestPackStore_PersistsAcrossReopen(t *testing.T) {
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

	// Same content (outputID) => second Put reuses the first body's offset.
	require.Equal(t, loc1.dataOff, loc2.dataOff)

	// The body is stored once; the second Put adds only a header-only alias
	// record (which is what makes the dedup survive a restart).
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
// when two actions share content (the very common empty-output case), the
// second is stored as an alias record. That alias MUST survive a reopen, or the
// next build misses it and falls through to the (slow) network tier.
func TestPackStore_DedupPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)

	empty := hexID(200) // outputID for empty content
	// Many actions, all producing identical (empty) content — only the first
	// writes a body; the rest are aliases.
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
	// The shared non-empty body is stored once (one full record + one alias),
	// not twice.
	info, err := os.Stat(s2.packPath(1))
	require.Nil(t, err)
	// 50 empty: 1 full (header only) + 49 alias (header only) = 50 headers.
	// shared: 1 full (header+body) + 1 alias (header). Total bytes:
	want := int64(50*packHeaderLen) + int64(packHeaderLen+len(body)) + int64(packHeaderLen)
	require.Equal(t, want, info.Size())
}

func TestPackStore_OverwriteAction(t *testing.T) {
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
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)
	_, err = s.Put(hexID(1), hexID(10), bytes.NewReader([]byte("good record")))
	require.Nil(t, err)
	require.Nil(t, s.Close())

	// Append a bogus record header that claims a body far past EOF — exactly
	// what a crash mid-append leaves behind.
	f, err := os.OpenFile(s.packPath(1), os.O_WRONLY|os.O_APPEND, 0o644)
	require.Nil(t, err)
	torn := make([]byte, packHeaderLen)
	binary.LittleEndian.PutUint32(torn[0:4], packRecordMagic)
	binary.LittleEndian.PutUint64(torn[12+2*hashLen:20+2*hashLen], 1<<40) // absurd dataLen
	_, err = f.Write(torn)
	require.Nil(t, err)
	require.Nil(t, f.Close())

	// Reopen must recover the good record and silently drop the torn one.
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

func TestPackStore_ResetWhenTooLarge(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)
	_, err = s.Put(hexID(1), hexID(10), bytes.NewReader(bytes.Repeat([]byte("z"), 4096)))
	require.Nil(t, err)
	require.Nil(t, s.Close())

	// Simulate a startup where the total pack size exceeds the reset bound.
	orig := packResetBytes
	packResetBytes = 100
	defer func() { packResetBytes = orig }()

	s2, err := OpenPackStore(dir)
	require.Nil(t, err)
	defer s2.Close()
	require.Equal(t, 0, s2.Len()) // store was reset to a cold cache
	_, ok := s2.Get(hexID(1))
	require.False(t, ok)
}

func TestParsePackName(t *testing.T) {
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
	_, err := decodeHash(hexID(1))
	require.Nil(t, err)
	_, err = decodeHash("tooshort")
	require.NotNil(t, err)
	_, err = decodeHash(strings.Repeat("z", 64))
	require.NotNil(t, err)
}

// packPath is exercised indirectly above; this guards the naming scheme.
func TestPackStore_PackPath(t *testing.T) {
	s := &PackStore{dir: "/tmp/x"}
	require.Equal(t, filepath.Join("/tmp/x", "pack-000001.data"), s.packPath(1))
}
