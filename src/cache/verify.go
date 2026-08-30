package cache

import (
	"fmt"
	"hash/crc32"
	"sync"
)

// This file holds the serve-path integrity gates shared by the pack store
// (pack.go) and the loose-file tier (local.go, via verifyBodyForServe): a
// served body must match its content hash and its stamped build-id action.
//
// The Go module index is refused here too (see isGoModuleIndex): its action
// key hashes no content, so no gate can tell a right body from a wrong body.
// This is safe only because handlePut refuses the index UP FRONT, so nothing
// bad is ever stored; this gate only sheds residue an older binary left.

// verifyBodyForServe checks a body about to be served for (actionID,
// outputID): content address (the sha256 matches outputID, see
// outputIDMatches), build-id action (a compiled package's stamped action
// matches actionID, see buildIDMatchesAction), and module index (never
// served). Returns ok == false with a human-readable eviction-log reason.
func verifyBodyForServe(actionID, outputID string, body []byte) (reason string, ok bool) {
	if isGoModuleIndex(body) {
		return "module index (never served: its action key carries no content to verify against)", false
	}
	if got, ok := outputIDMatches(outputID, body); !ok {
		return fmt.Sprintf("body checksum mismatch (want outputid=%s, got sha256=%s, len=%d)",
			shortID(outputID), shortID(got), len(body)), false
	}
	if act, ok := buildIDMatchesAction(actionID, body); !ok {
		return fmt.Sprintf("build-id action mismatch (want action=%s, got action=%s, len=%d)",
			expectedBuildIDAction(actionID), act, len(body)), false
	}
	return "", true
}

// looseVerified memoizes a loose entry that already passed verification,
// valid while the sidecar still names this outputID and size.
type looseVerified struct {
	outputID string
	size     int64
}

// Verified-read memo: pack records are immutable after they are appended, so
// (packID, dataOff) is a stable key for the store's lifetime.

// verifyKey names a pack record by its physical location.
type verifyKey struct {
	packID  int
	dataOff int64
}

// verifyInfo memoizes every fact the serve gates need about a record's body,
// computed from a single body read (or from the in-memory bytes at Put time). The
// fields are FACTS about the bytes, not verdicts: each serve path applies its
// own gate over them, so per-action decisions (an aliased archive stamped for
// a different action) stay correct on memo hits.
type verifyInfo struct {
	crcOK        bool   // body matched the record-header CRC (rot-free w.r.t. append time)
	shaOK        bool   // body hashed (sha256) to loc.outputID (content address proven)
	isPkgArchive bool   // ar archive with a __.PKGDEF member
	isModIndex   bool   // Go module index blob (never servable — see modindex.go)
	stampAction  string // build-id action field ("" when none)
}

// actionOK replicates buildIDMatchesAction's decision from memoized facts.
// Keep in lockstep with buildIDMatchesAction (buildid.go) — the pack tests
// exercise both paths against the same poison shapes.
func (vi verifyInfo) actionOK(actionIDHex string) bool {
	want := expectedBuildIDAction(actionIDHex)
	if vi.stampAction != "" {
		if want == "" {
			return true // no derivable expectation; don't false-positive
		}
		return vi.stampAction == want
	}
	if vi.isPkgArchive && want != "" {
		return false // package archive with no build id: corrupt or stripped
	}
	return true
}

// servableForAction is the GET-RPC gate: body intact (CRC or the stronger
// content-address proof), not a module index, and stamped for this action.
func (vi verifyInfo) servableForAction(actionIDHex string) bool {
	if vi.isModIndex {
		return false
	}
	if !vi.crcOK && !vi.shaOK {
		return false
	}
	return vi.actionOK(actionIDHex)
}

// verifiedSet is the concurrency-safe (packID, dataOff) -> verifyInfo memo.
type verifiedSet struct {
	mu sync.RWMutex
	m  map[verifyKey]verifyInfo
}

func (v *verifiedSet) get(k verifyKey) (verifyInfo, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	vi, ok := v.m[k]
	return vi, ok
}

func (v *verifiedSet) put(k verifyKey, vi verifyInfo) {
	v.mu.Lock()
	if v.m == nil {
		v.m = make(map[verifyKey]verifyInfo)
	}
	v.m[k] = vi
	v.mu.Unlock()
}

// dropPack forgets every memoized record in packID, so a later pack reusing
// the ID never inherits stale entries.
func (v *verifiedSet) dropPack(packID int) {
	v.mu.Lock()
	for k := range v.m {
		if k.packID == packID {
			delete(v.m, k)
		}
	}
	v.mu.Unlock()
}

// verifyRecordFull reads loc's body a single time and computes every serve-gate fact.
// Returns ok == false on a read/map failure (nothing can be said about the
// body; callers treat it as corrupt and do not memoize).
func (s *PackStore) verifyRecordFull(loc packLoc) (verifyInfo, bool) {
	var vi verifyInfo
	ok := s.verifyBody(loc, func(body []byte) bool {
		vi.crcOK = crc32.Checksum(body, packCRC) == loc.crc
		_, vi.shaOK = outputIDMatches(loc.outputID, body)
		vi.isModIndex = isGoModuleIndex(body)
		vi.isPkgArchive, vi.stampAction = archiveExportInfo(body)
		return true // facts recorded; gates are applied by the callers
	})
	return vi, ok
}

// verifiedInfo returns the memoized facts for loc, computing and memoizing
// them with a single body read on the earliest access.
func (s *PackStore) verifiedInfo(loc packLoc) (verifyInfo, bool) {
	k := verifyKey{packID: loc.packID, dataOff: loc.dataOff}
	if vi, ok := s.verified.get(k); ok {
		return vi, true
	}
	vi, ok := s.verifyRecordFull(loc)
	if !ok {
		return verifyInfo{}, false
	}
	s.verified.put(k, vi)
	return vi, true
}

// verifyInfoForPut computes the memo entry for bytes appended by Put: CRC
// holds by construction, and the content address is rechecked directly so a
// mis-addressed Put is never pre-trusted.
func verifyInfoForPut(outputID string, data []byte) verifyInfo {
	vi := verifyInfo{crcOK: true}
	_, vi.shaOK = outputIDMatches(outputID, data)
	vi.isModIndex = isGoModuleIndex(data)
	vi.isPkgArchive, vi.stampAction = archiveExportInfo(data)
	return vi
}
