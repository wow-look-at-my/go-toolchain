package cache

import (
	"hash/crc32"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// GetByOutputVerified is GetByOutput with a content-address integrity gate on
// the path that actually feeds the compiler: the GET RPC's DiskPath is read
// directly through the FUSE mount, bypassing GetVerified's RPC-level check.
// It verifies the body's SHA-256 against outputID (the content-address
// invariant), stronger than the pack CRC -- it also catches a torn or
// mis-mapped record that is CRC-consistent with itself but wrong content.
// A mismatch evicts the entry and reports not-found, so the mount returns
// ENOENT and go recomputes instead of consuming a damaged object. Like
// GetByOutput it does not count as a hit -- the originating Get already did.
func (s *PackStore) GetByOutputVerified(outputID string) (packLoc, bool) {
	s.mu.RLock()
	loc, ok := s.byOutput[outputID]
	s.mu.RUnlock()
	if !ok {
		return packLoc{}, false
	}
	// This path requires the content-address proof (shaOK) from the same memo.
	vi, ok := s.verifiedInfo(loc)
	if !ok || !vi.shaOK {
		s.evictCorruptByOutput(outputID, loc)
		s.Stats.Corrupt.Increment()
		// Deliberate poison-refusal: eviction turns the already-promised DiskPath into ENOENT on next open.
		logger.Warn("cacheprog: local pack: refusing corrupt body for output %s; evicted (a previously promised DiskPath for it will now open as ENOENT)",
			shortID(outputID))
		return packLoc{}, false
	}
	return loc, true
}

// GetVerified is like Get, but first confirms the body matches its header CRC; a mismatch evicts and misses.
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
	// Per-action gate: an archive stamped for a different action is refused even on a memo hit.
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

// verifyBody runs check over loc's stored body. Large bodies are verified via
// an mmap of the pack region (see mmapVerifyThreshold); small bodies take a
// plain read. A read or map error counts as a failure. check must not retain
// the slice past its return -- a mapped body is unmapped once verifyBody returns.
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

// bodyMatchesCRC reports whether loc's body hashes to its recorded CRC; kept
// for tests -- serve paths use the memoized verifyInfo instead.
func (s *PackStore) bodyMatchesCRC(loc packLoc) bool {
	return s.verifyBody(loc, func(body []byte) bool {
		return crc32.Checksum(body, packCRC) == loc.crc
	})
}

// evictCorrupt drops a corrupt entry from the in-memory index; dead bytes stay
// in the pack until reset. Only the exact location is removed, so a
// concurrent re-Put is left intact.
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

// evictCorruptByOutput is evictCorrupt keyed by outputID instead of actionID;
// any action still mapped to the bytes self-heals on the next GetVerified.
func (s *PackStore) evictCorruptByOutput(outputID string, loc packLoc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.byOutput[outputID]; ok && cur == loc {
		delete(s.byOutput, outputID)
	}
}
