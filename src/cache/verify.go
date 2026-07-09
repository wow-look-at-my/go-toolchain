package cache

import (
	"fmt"
	"hash/crc32"
	"sync"
)

// This file holds the shared serve-path integrity gates used by the local
// cache tiers. The pack store (pack.go) runs its gates over pack records; the
// loose-file tier (local.go) runs verifyBodyForServe over each entry before
// serving it. Both enforce the rot and build-id invariants the web ingestion
// path also does: the cache never hands the compiler a damaged body, nor a
// compiled package stamped for a different action key.
//
// The one deliberate divergence from the web tier is the Go module index: the
// LOCAL tiers serve it, the web tier refuses it everywhere. Every web->local
// ingestion path refuses module indexes (web.go individual GET, batch.go batch
// GET, cache.go prefetch population) and uploads refuse them too (webput.go),
// so anything with the "go index v" magic in the local store was computed by
// the local cmd/go under its own action key — the same trust upstream GOCACHE
// places in its own directory — and its body is still SHA-256-verified against
// outputID. Refusing it here (as this file once did) created a permanent
// accept-at-Put/refuse-at-Get loop: cmd/go stores hundreds of per-directory
// index blobs through the cacheprog on every invocation, each Put was
// accepted, each Get refused-and-evicted, so every index key missed on every
// build forever (log spam on the loose tier, duplicate-record append churn and
// unbounded pack growth on the pack tier). Pre-guard residue — packs populated
// by binaries older than the web-ingestion guards, which could hold
// web-originated (potentially mis-keyed) indexes — is flushed by the one-time
// local cache version purge (see cacheversion.go).

// verifyBodyForServe runs the local serve-path integrity gates over an
// in-memory body about to be served for (actionID, outputID):
//
//   - content address: the body must hash (SHA-256) to outputID — the
//     GOCACHEPROG invariant (outputID == sha256(body), see outputIDMatches).
//     Catches truncation, rot, and mis-mapped bodies, including the empty
//     bodies the old oversized-PUT bug committed under real IDs.
//   - build-id action: a compiled package's stamped build-id action must
//     belong to actionID (see buildIDMatchesAction). Catches a self-consistent
//     object filed under the wrong action key ("runtime imported as
//     reflectlite").
//
// A Go module index is deliberately servable from the local tier: it is
// locally-originated by construction (web ingestion refuses index blobs on
// every path — see modindex.go, webput.go, and the prefetch filter in
// cache.go) and the sha256 gate above still rot-protects it; pre-guard residue
// is flushed by the one-time version purge (cacheversion.go). See the file-top
// comment.
//
// On failure it returns ok == false and a human-readable reason for the
// eviction log line.
func verifyBodyForServe(actionID, outputID string, body []byte) (reason string, ok bool) {
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

// looseVerified records a loose-tier entry that passed verifyBodyForServe
// this process, so repeat Gets (notably the PUT dedup check on warm rebuilds)
// skip the body re-read + re-hash. An entry only short-circuits verification
// while the on-disk sidecar still advertises the same outputID and the data
// file still has the verified size; any replacement re-verifies.
type looseVerified struct {
	outputID string
	size     int64
}

// ---- pack-store verified-read memoization ----
//
// Every GET RPC used to re-read + CRC + guard-check the FULL body
// (bodyServableForAction), and every first FUSE Lookup per outputID re-read +
// SHA-256'd it (bodyMatchesOutputID) — including immediately after every PUT
// (the compiler opens the DiskPath the PUT just returned) and after remote
// ingestion that was already sha256-verified at the network boundary. A
// pipeline run is 4-6 cmd/go invocations re-GETting the same actions, so the
// same bytes were read and hashed many times per build.
//
// Memoizing is sound because pack records are physically immutable within a
// process: packs are append-only, records are never rewritten in place,
// truncation happens only at startup before serving, and eviction only
// removes index entries. A (packID, dataOff) therefore names one fixed byte
// range for the store's lifetime. The residual TOCTOU — bytes rotting on disk
// AFTER a successful verification — is identical to what the kernel page
// cache + FOPEN_KEEP_CACHE already accept on the FUSE read path (rot across
// runs is still caught: a new process starts with an empty memo).

// verifyKey names a pack record by its physical location.
type verifyKey struct {
	packID  int
	dataOff int64
}

// verifyInfo memoizes every fact the serve gates need about a record's body,
// computed from one body read (or from the in-memory bytes at Put time). The
// fields are FACTS about the bytes, not verdicts: each serve path applies its
// own gate over them, so per-action decisions (an aliased archive stamped for
// a different action) stay correct on memo hits.
type verifyInfo struct {
	crcOK        bool   // body matched the record-header CRC (rot-free w.r.t. append time)
	shaOK        bool   // body hashed (SHA-256) to loc.outputID (content address proven)
	isPkgArchive bool   // ar archive with a __.PKGDEF member
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

// servableForAction is the GET-RPC gate (the memoized equivalent of
// bodyServableForAction): the body must be intact (CRC, or the strictly
// stronger content-address proof) and a compiled package must be stamped for
// this action. A module index passes: locally-originated by construction (the
// web ingestion paths refuse index blobs — see the file-top comment), so
// refusing it here only recreated the permanent store/refuse miss loop.
func (vi verifyInfo) servableForAction(actionIDHex string) bool {
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

// dropPack forgets every memoized record in packID. Called when a pack file
// is deleted (startup budget eviction), so a later pack reusing the ID can
// never inherit stale entries.
func (v *verifiedSet) dropPack(packID int) {
	v.mu.Lock()
	for k := range v.m {
		if k.packID == packID {
			delete(v.m, k)
		}
	}
	v.mu.Unlock()
}

// verifyRecordFull reads loc's body once and computes every serve-gate fact.
// Returns ok == false on a read/map failure (nothing can be said about the
// body; callers treat it as corrupt and do not memoize).
func (s *PackStore) verifyRecordFull(loc packLoc) (verifyInfo, bool) {
	var vi verifyInfo
	ok := s.verifyBody(loc, func(body []byte) bool {
		vi.crcOK = crc32.Checksum(body, packCRC) == loc.crc
		_, vi.shaOK = outputIDMatches(loc.outputID, body)
		vi.isPkgArchive, vi.stampAction = archiveExportInfo(body)
		return true // facts recorded; gates are applied by the callers
	})
	return vi, ok
}

// verifiedInfo returns the memoized facts for loc, computing and memoizing
// them with one body read on first access.
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

// verifyInfoForPut computes the memo entry for bytes being appended by Put:
// the CRC is by construction computed from these exact bytes, the content
// address is checked directly (cmd/go derives outputID as sha256(body), and
// every remote-ingestion path verified it at the network boundary — this
// recheck keeps a mis-addressed direct Put from being pre-trusted), and the
// structural facts come from the same in-memory buffer. This is what makes
// the compiler's open-right-after-PUT lookup free of a full re-read + hash.
func verifyInfoForPut(outputID string, data []byte) verifyInfo {
	vi := verifyInfo{crcOK: true}
	_, vi.shaOK = outputIDMatches(outputID, data)
	vi.isPkgArchive, vi.stampAction = archiveExportInfo(data)
	return vi
}
