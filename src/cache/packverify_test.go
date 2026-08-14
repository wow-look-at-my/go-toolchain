package cache

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPackStore_GetVerifiedDetectsCorruptBody(t *testing.T) {
	// Rot detection is a CROSS-PROCESS property: within one process a record
	// that verified once is memoized (records are physically immutable; see
	// verify.go), so this test corrupts the pack between a Close and a
	// reopen — how rot actually manifests — and the fresh process must refuse.
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)

	aid := hexID(7)
	body := []byte("a body that must never be served if corrupted")
	oid := casID(body)
	loc, err := s.Put(aid, oid, bytes.NewReader(body))
	require.Nil(t, err)

	// A clean body verifies and is served as a hit.
	got, ok := s.GetVerified(aid)
	require.True(t, ok)
	require.Equal(t, oid, got.outputID)
	require.Nil(t, s.Close())

	// Corrupt one body byte in place: length and header (and thus the recorded
	// CRC) are untouched, so the scan still indexes it — exactly the case the
	// torn-tail check cannot catch (disk/overlay rot, or a partial overwrite).
	f, err := os.OpenFile(filepath.Join(dir, "pack-000001.data"), os.O_RDWR, 0o644)
	require.Nil(t, err)
	var b [1]byte
	_, err = f.ReadAt(b[:], loc.dataOff)
	require.Nil(t, err)
	_, err = f.WriteAt([]byte{b[0] ^ 0xff}, loc.dataOff)
	require.Nil(t, err)
	require.Nil(t, f.Close())

	// The corrupt body must NOT be served: report a miss, bump the corruption
	// counter, and evict from both the action and output indexes.
	s2, err := OpenPackStore(dir)
	require.Nil(t, err)
	defer s2.Close()
	_, ok = s2.GetVerified(aid)
	require.False(t, ok, "corrupt body must not be served as a hit")
	require.Equal(t, uint32(1), s2.Stats.Corrupt.Load())
	_, ok = s2.GetVerified(aid)
	require.False(t, ok, "evicted entry stays a miss")
	_, ok = s2.GetByOutput(oid)
	require.False(t, ok, "corrupt body evicted from the output index too")

	// A fresh Put for the same action heals the cache.
	_, err = s2.Put(aid, oid, bytes.NewReader(body))
	require.Nil(t, err)
	got, ok = s2.GetVerified(aid)
	require.True(t, ok, "re-Put clean body is served again")
	data, err := s2.ReadAll(got)
	require.Nil(t, err)
	require.True(t, bytes.Equal(body, data))
}

func TestPackStore_GetVerifiedDetectsCorruptLargeBody(t *testing.T) {
	// Cross-process rot on the mmap verification path (see the sibling test
	// for why corruption is applied across a Close/reopen).
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)

	aid := hexID(9)
	// Larger than mmapVerifyThreshold so verification takes the mmap path (a
	// page-aligned region map indexed into), not the small-body read.
	body := bytes.Repeat([]byte("go-toolchain pack-cache integrity probe; "), 4096) // ~168 KiB
	require.Greater(t, len(body), mmapVerifyThreshold)
	oid := casID(body)
	loc, err := s.Put(aid, oid, bytes.NewReader(body))
	require.Nil(t, err)

	// Clean large body verifies via mmap.
	_, ok := s.GetVerified(aid)
	require.True(t, ok)
	require.Nil(t, s.Close())

	// Corrupt the final body byte: it sits well past the first page, so the
	// page-alignment/offset math and the full mapped span are both exercised.
	f, err := os.OpenFile(filepath.Join(dir, "pack-000001.data"), os.O_RDWR, 0o644)
	require.Nil(t, err)
	pos := loc.dataOff + loc.dataLen - 1
	var b [1]byte
	_, err = f.ReadAt(b[:], pos)
	require.Nil(t, err)
	_, err = f.WriteAt([]byte{b[0] ^ 0xff}, pos)
	require.Nil(t, err)
	require.Nil(t, f.Close())

	s2, err := OpenPackStore(dir)
	require.Nil(t, err)
	defer s2.Close()
	_, ok = s2.GetVerified(aid)
	require.False(t, ok, "corrupt large body must not be served (mmap verify path)")
	require.Equal(t, uint32(1), s2.Stats.Corrupt.Load())
}

// TestPackStore_GetVerifiedRefusesCrossContaminatedPackage is the local
// serve-path counterpart of the web build-id guard: a compiled package whose
// build id belongs to a DIFFERENT action than the key it is stored under (the
// "runtime imported as reflectlite" poison) must be refused, even though its
// bytes are self-consistent (clean CRC). This is what lets a poisoned local pack
// self-heal — the entry is evicted and the GET misses, so the toolchain
// recomputes instead of being handed the wrong package's export data.
func TestPackStore_GetVerifiedRefusesCrossContaminatedPackage(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)
	defer s.Close()

	actionA, actionB := hexID(20), hexID(99)
	oid := hexID(120)
	poison := archiveWithBuildID(expectedBuildIDAction(actionB)) // stamped for B
	_, err = s.Put(actionA, oid, bytes.NewReader(poison))        // stored under A
	require.Nil(t, err)

	_, ok := s.GetVerified(actionA)
	require.False(t, ok, "a package whose build id belongs to another action must not be served")
	require.Equal(t, uint32(1), s.Stats.Corrupt.Load())
	_, ok = s.GetByOutput(oid)
	require.False(t, ok, "cross-contaminated body evicted from the output index too")

	// A correctly build-id-stamped package for the SAME action IS served — the
	// guard refuses only mis-keyed objects, never legitimate cache hits.
	good := archiveWithBuildID(expectedBuildIDAction(actionA))
	goodOID := hexID(121)
	_, err = s.Put(actionA, goodOID, bytes.NewReader(good))
	require.Nil(t, err)
	_, ok = s.GetVerified(actionA)
	require.True(t, ok, "a correctly-keyed package must still be served")
}

// TestPackStore_GetVerifiedRefusesModuleIndex pins the pack tier's half of the
// module-index policy: the GET RPC never serves one, so residue a binary that
// predates the PUT refusal left in the packs cannot be handed back. An ordinary
// body in the same pack is unaffected — the refusal is about the payload, not
// about the store.
func TestPackStore_GetVerifiedRefusesModuleIndex(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)
	defer s.Close()

	index := []byte("go index v2\n\x00\x00module-index payload from an older binary")
	aid, oid := hexID(40), casID(index) // content-addressed, as cmd/go stores it
	_, err = s.Put(aid, oid, bytes.NewReader(index))
	require.Nil(t, err)

	_, ok := s.GetVerified(aid)
	require.False(t, ok, "a stored module index must never be served from the local pack")

	plain := []byte("an ordinary cached body sharing the pack")
	paid, poid := hexID(41), casID(plain)
	_, err = s.Put(paid, poid, bytes.NewReader(plain))
	require.Nil(t, err)
	loc, ok := s.GetVerified(paid)
	require.True(t, ok, "an ordinary body must still be served")
	data, err := s.ReadAll(loc)
	require.Nil(t, err)
	require.Equal(t, plain, data)
}

func TestPackStore_GetByOutputVerifiedDetectsCorruptBody(t *testing.T) {
	// Cross-process rot on the compiler's serve path (see the GetVerified
	// sibling test for why corruption is applied across a Close/reopen: a
	// record that verified once is memoized within a process).
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)

	body := []byte("a body resolved by outputID on the compiler's serve path")
	aid, oid := hexID(11), casID(body)
	loc, err := s.Put(aid, oid, bytes.NewReader(body))
	require.Nil(t, err)

	// A clean body resolves by output and verifies.
	got, ok := s.GetByOutputVerified(oid)
	require.True(t, ok)
	require.Equal(t, oid, got.outputID)
	require.Nil(t, s.Close())

	// Rot one body byte in place: header length + recorded CRC stay intact, so
	// the startup scan still indexes it (the disk/overlay-rot case the torn-tail
	// check cannot catch).
	f, err := os.OpenFile(filepath.Join(dir, "pack-000001.data"), os.O_RDWR, 0o644)
	require.Nil(t, err)
	var b [1]byte
	_, err = f.ReadAt(b[:], loc.dataOff)
	require.Nil(t, err)
	_, err = f.WriteAt([]byte{b[0] ^ 0xff}, loc.dataOff)
	require.Nil(t, err)
	require.Nil(t, f.Close())

	s2, err := OpenPackStore(dir)
	require.Nil(t, err)
	defer s2.Close()

	// The unverified accessor (what Lookup used to call) cannot detect the rot —
	// it would hand these bytes to the compiler. This is the gap.
	_, stillThere := s2.GetByOutput(oid)
	require.True(t, stillThere, "unverified GetByOutput cannot detect in-place rot")

	// The verified accessor refuses it: report a miss, bump the corruption
	// counter, and evict from the output index so the mount stops serving it.
	_, ok = s2.GetByOutputVerified(oid)
	require.False(t, ok, "corrupt body must not be served on the verified serve path")
	require.Equal(t, uint32(1), s2.Stats.Corrupt.Load())
	_, ok = s2.GetByOutput(oid)
	require.False(t, ok, "corrupt body evicted from the output index")
}

// TestPackStore_MemoizedVerificationStillChecksActionPerKey guards the one
// subtlety of the verified-read memo: facts are memoized per RECORD, but the
// build-id action gate is per KEY. Two actions aliased to the same archive
// body (content dedup) must get independent verdicts — serving action B a
// package stamped for action A on a memo hit would be cross-contamination.
func TestPackStore_MemoizedVerificationStillChecksActionPerKey(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)
	defer s.Close()

	actionA, actionB := hexID(20), hexID(99)
	archive := archiveWithBuildID(expectedBuildIDAction(actionA)) // stamped for A
	oid := casID(archive)

	_, err = s.Put(actionA, oid, bytes.NewReader(archive))
	require.Nil(t, err)
	// Same content under action B: stored as an alias to the same record.
	_, err = s.Put(actionB, oid, bytes.NewReader(archive))
	require.Nil(t, err)

	// Action A verifies and memoizes the record's facts.
	_, ok := s.GetVerified(actionA)
	require.True(t, ok, "the correctly-stamped action must be served")

	// Action B resolves to the SAME memoized record but must be refused: the
	// stamp belongs to A.
	_, ok = s.GetVerified(actionB)
	require.False(t, ok, "an aliased archive stamped for another action must be refused on a memo hit")
	require.Equal(t, uint32(1), s.Stats.Corrupt.Load())

	// And action A must still be served afterwards (B's eviction only removed
	// B's mapping).
	_, ok = s.GetVerified(actionA)
	require.True(t, ok, "the stamped action must remain served after the alias refusal")
}

// TestPackStore_GetByOutputVerifiedRejectsContentMismatch is the regression for
// the serve-path gap the CRC gate could not close: a record whose body is
// self-consistent with its own recorded CRC but is NOT the content addressed by
// the outputID it is served under (a torn or mis-mapped record, or a poisoned
// remote object). The CRC gate would hand those bytes to the compiler, surfacing
// as "corrupt index" / "package ... is not in std" for a module index. The
// content-address (SHA-256) gate must refuse and evict it.
func TestPackStore_GetByOutputVerifiedRejectsContentMismatch(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.Nil(t, err)
	defer s.Close()

	body := []byte("module-index bytes that do not hash to the outputID they are served under")
	// Store the body under an outputID that is NOT sha256(body). Put does not
	// enforce the invariant, so the record lands self-consistent with its own CRC
	// — the exact shape the CRC gate cannot detect.
	wrongOID := hexID(200)
	require.NotEqual(t, casID(body), wrongOID)
	loc, err := s.Put(hexID(1), wrongOID, bytes.NewReader(body))
	require.Nil(t, err)

	// The CRC gate would accept it: the body matches its own recorded CRC, and the
	// unverified accessor still resolves it.
	require.True(t, s.bodyMatchesCRC(loc), "record is self-consistent with its CRC")
	_, ok := s.GetByOutput(wrongOID)
	require.True(t, ok)

	// The content-address gate refuses it, bumps the corruption counter, and
	// evicts it so the mount stops serving it.
	_, ok = s.GetByOutputVerified(wrongOID)
	require.False(t, ok, "body that does not hash to its outputID must not be served")
	require.Equal(t, uint32(1), s.Stats.Corrupt.Load())
	_, ok = s.GetByOutput(wrongOID)
	require.False(t, ok, "content-mismatched body evicted from the output index")

	// A content-addressed entry for the same bytes IS served.
	goodOID := casID(body)
	_, err = s.Put(hexID(2), goodOID, bytes.NewReader(body))
	require.Nil(t, err)
	got, ok := s.GetByOutputVerified(goodOID)
	require.True(t, ok, "a body that hashes to its outputID is served")
	require.Equal(t, goodOID, got.outputID)
}
