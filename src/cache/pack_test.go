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

	// The pack file therefore holds exactly one record, not two.
	info, err := os.Stat(s.packPath(1))
	require.Nil(t, err)
	require.Equal(t, int64(packHeaderLen+len(body)), info.Size())

	// Both actions resolve to the same bytes.
	for _, aid := range []string{hexID(1), hexID(2)} {
		got, ok := s.Get(aid)
		require.True(t, ok)
		data, err := s.ReadAll(got)
		require.Nil(t, err)
		require.Equal(t, body, data)
	}
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
