package cache

import (
	"hash/crc32"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// GetByOutputVerified is GetByOutput with a content-address integrity gate,
// applied on the path that actually feeds the compiler.
//
// GetVerified guards the GET *RPC*, but the Go toolchain does not consume bytes
// over that RPC: the GET response hands it a DiskPath, and the compiler opens
// that path and reads the body through the FUSE mount (Lookup -> Read). That
// read resolves the body via this method, not via GetVerified — so without a
// check here the integrity guard is bypassed for exactly the bytes the compiler
// reads. This gate verifies the body's SHA-256 against the requested outputID
// (the content address: outputID == sha256(body) is the GOCACHEPROG invariant),
// which is strictly stronger than the pack CRC: it catches not only disk/overlay
// rot but also a torn or mis-mapped record whose bytes are self-consistent with
// their own recorded CRC yet are not the content asked for — the case that
// otherwise reaches the compiler as "unexpected EOF" / "corrupt index" /
// "package ... is not in std" (a poisoned module index). It is the local
// serve-path counterpart of the end-to-end hash the web ingestion path already
// enforces (integrity.go). A mismatch evicts the entry from the output index and
// reports not-found, so the mount returns ENOENT and the go command recomputes
// instead of consuming a damaged object. Like GetByOutput it does not count as a
// hit — the originating Get already did.
func (s *PackStore) GetByOutputVerified(outputID string) (packLoc, bool) {
	s.mu.RLock()
	loc, ok := s.byOutput[outputID]
	s.mu.RUnlock()
	if !ok {
		return packLoc{}, false
	}
	// One memoized fact set serves both gates; this path requires the
	// content-address proof (shaOK), exactly as bodyMatchesOutputID did.
	// (byOutput's invariant guarantees loc.outputID == outputID, so the memo's
	// sha result is against the id being served.)
	vi, ok := s.verifiedInfo(loc)
	if !ok || !vi.shaOK {
		s.evictCorruptByOutput(outputID, loc)
		s.Stats.Corrupt.Increment()
		// This is the FUSE serve path: a GET response already promised this
		// DiskPath to the toolchain, and the eviction turns its next open
		// into ENOENT. Deliberate poison-refusal trade-off; make it visible.
		logger.Warn("cacheprog: local pack: refusing corrupt body for output %s; evicted (a previously promised DiskPath for it will now open as ENOENT)",
			shortID(outputID))
		return packLoc{}, false
	}
	return loc, true
}

// GetVerified is like Get but first confirms the stored body still matches the
// CRC recorded in its header. A build cache must never hand back corrupt bytes:
// a damaged body (a torn append that nonetheless landed full-length, disk or
// overlay bit-rot, a bad archive ingested from the remote tier) would be fed to
// the Go toolchain as a valid object — e.g. a module index, which then fails the
// build with "corrupt index", an error go cannot recover from in-process. So a
// mismatch evicts the entry and reports a miss, letting the toolchain recompute
// and re-Put clean data instead of consuming garbage.
func (s *PackStore) GetVerified(actionID string) (packLoc, bool) {
	return s.getVerifiedCounted(actionID, true)
}

// PeekVerified is GetVerified without counting a hit — the PUT dedup lookup.
func (s *PackStore) PeekVerified(actionID string) (packLoc, bool) {
	return s.getVerifiedCounted(actionID, false)
}

func (s *PackStore) getVerifiedCounted(actionID string, countHit bool) (packLoc, bool) {
	s.mu.RLock()
	loc, ok := s.byAction[actionID]
	s.mu.RUnlock()
	if !ok {
		return packLoc{}, false
	}
	// The serve-gate facts — rot (CRC/content address) and build-id action —
	// come from one memoized body read (first access this process, or free at
	// Put time; see verify.go). The per-ACTION gate is still applied on every
	// call: facts are content properties, but whether a stamped archive
	// belongs under THIS action depends on the key, so an aliased archive
	// stamped for a different action is refused even on a memo hit. Any
	// failure evicts the entry and reports a miss, so the toolchain recomputes
	// clean data instead of being handed poison — the local-tier counterpart
	// of the web ingestion guards. A module index fails the gate outright; with
	// the PUT refusal in place, only an older binary's residue can reach it.
	vi, ok := s.verifiedInfo(loc)
	if !ok || !vi.servableForAction(actionID) {
		s.evictCorrupt(actionID, loc)
		s.Stats.Corrupt.Increment()
		return packLoc{}, false
	}
	if countHit {
		s.Stats.Hits.Increment()
	}
	return loc, true
}

// verifyBody runs check over loc's stored body and returns its result. Large
// bodies are verified over an mmap of the pack region so they are never copied
// onto the heap on every hit (see mmapVerifyThreshold); small bodies take a
// plain read, whose allocation is negligible and cheaper than the mmap/munmap
// syscalls. A read or map error counts as a failure. check must not retain the
// slice past its return — for a mapped body the region is unmapped on return.
// mmap offsets must be page-aligned, so it maps from the page boundary at or
// before the body and indexes in.
func (s *PackStore) verifyBody(loc packLoc, check func(body []byte) bool) bool {
	if loc.dataLen >= mmapVerifyThreshold && loc.dataLen <= int64(maxInt) {
		if f := s.pack(loc.packID); f != nil {
			if body, release, ok := mapPackSpan(f, loc.dataOff, loc.dataLen); ok {
				defer release()
				return check(body)
			}
			// mmap unavailable (map error, or no mmap port for this GOOS):
			// fall back to a read below.
		}
	}
	body, err := s.ReadAll(loc)
	if err != nil {
		return false
	}
	return check(body)
}

// bodyMatchesCRC reports whether loc's body still hashes to the CRC recorded
// in its record header. The serve paths consume this fact via the memoized
// verifyInfo (verify.go); this direct form remains for tests that need to
// interrogate a record's raw CRC state.
func (s *PackStore) bodyMatchesCRC(loc packLoc) bool {
	return s.verifyBody(loc, func(body []byte) bool {
		return crc32.Checksum(body, packCRC) == loc.crc
	})
}

// evictCorrupt drops a corrupt entry from the in-memory index so it is never
// served again this process. The dead bytes stay in the pack (unreferenced)
// until the next reset — correctness, not space, is the priority. Only the exact
// location is removed, so a concurrent re-Put that already replaced the mapping
// is left intact.
func (s *PackStore) evictCorrupt(actionID string, loc packLoc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.byAction[actionID]; ok && cur == loc {
		delete(s.byAction, actionID)
	}
	if cur, ok := s.byOutput[loc.outputID]; ok && cur == loc {
		delete(s.byOutput, loc.outputID)
	}
}

// evictCorruptByOutput drops a corrupt entry from the output index so the mount
// stops serving it. It is the serve-path counterpart to evictCorrupt, which is
// keyed by actionID; here only the outputID is known. Any action still mapped to
// the same bytes is cleaned up by GetVerified on its next GET RPC (which
// re-checks the CRC and misses), so the entry self-heals on the following build.
// Only the exact location is removed, so a concurrent re-Put that already
// replaced the mapping is left intact.
func (s *PackStore) evictCorruptByOutput(outputID string, loc packLoc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.byOutput[outputID]; ok && cur == loc {
		delete(s.byOutput, outputID)
	}
}
