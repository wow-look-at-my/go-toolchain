package cache

// This file holds the pack store's write path: Put, PutIfAbsent, and the
// append helpers. See pack.go for the store and the read path.

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"io"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// Put stores body under actionID/outputID and returns its location.
//
// Three cases, cheapest first:
//   - The action already maps to this exact content (a warm-build re-populate):
//     nothing is written.
//   - The content (outputID) is already stored under some other action: a tiny
//     alias record (actionID -> outputID) is appended. This persists the dedup
//     so the mapping survives a restart — the bug that, when this was an
//     in-memory-only shortcut, lost thousands of empty-output actions on every
//     warm build and sent them to the network.
//   - New content: a full record (header + body) is appended.
func (s *PackStore) Put(actionID, outputID string, body io.Reader) (packLoc, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return packLoc{}, fmt.Errorf("pack put read: %w", err)
	}
	aid, err := decodeHash(actionID)
	if err != nil {
		return packLoc{}, fmt.Errorf("pack put action id: %w", err)
	}
	oid, err := decodeHash(outputID)
	if err != nil {
		return packLoc{}, fmt.Errorf("pack put output id: %w", err)
	}

	s.mu.RLock()
	prev, prevOK := s.byAction[actionID]
	existing, existOK := s.byOutput[outputID]
	s.mu.RUnlock()

	// Already persisted exactly this mapping: nothing to do.
	if prevOK && prev.outputID == outputID && prev.dataLen == int64(len(data)) {
		s.Stats.Puts.Increment()
		return prev, nil
	}

	// Content already stored: persist an alias record (no body) and point the
	// action at the existing body.
	if existOK && existing.dataLen == int64(len(data)) {
		loc := existing
		loc.created = packNow()
		_, _, err := s.appendRecordLoc(packAliasMagic, aid, oid, nil, nil, func(packLoc) {
			s.mu.Lock()
			s.byAction[actionID] = loc
			s.mu.Unlock()
		})
		if err != nil {
			return packLoc{}, err
		}
		s.Stats.Puts.Increment()
		return loc, nil
	}

	// Commit hook runs under the append lock, so index order always matches pack-file write order.
	var loc packLoc
	_, _, err = s.appendRecordLoc(packRecordMagic, aid, oid, data, nil, func(l packLoc) {
		loc = l
		// Pre-memoize serve-gate facts from the written bytes, so later reads skip a re-hash.
		s.verified.put(verifyKey{packID: l.packID, dataOff: l.dataOff}, verifyInfoForPut(outputID, data))
		s.mu.Lock()
		s.byAction[actionID] = l
		s.byOutput[outputID] = l
		s.mu.Unlock()
	})
	if err != nil {
		return packLoc{}, err
	}
	s.Stats.Puts.Increment()
	return loc, nil
}

// PutIfAbsent stores body under actionID/outputID only if the action is not
// already indexed, and reports whether this call stored it. The absence check
// runs under the same append lock as the write and index commit, so it is
// atomic with respect to every other Put/PutIfAbsent: an existing mapping —
// in particular one the local cmd/go just stored — is never replaced, in the
// live index or in the pack file's replay order. This is the primitive the
// web-prefetch population uses: a web-originated body must never displace a
// locally-originated entry (the module-index trust model in verify.go depends
// on it).
func (s *PackStore) PutIfAbsent(actionID, outputID string, body io.Reader) (packLoc, bool, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return packLoc{}, false, fmt.Errorf("pack put read: %w", err)
	}
	aid, err := decodeHash(actionID)
	if err != nil {
		return packLoc{}, false, fmt.Errorf("pack put action id: %w", err)
	}
	oid, err := decodeHash(outputID)
	if err != nil {
		return packLoc{}, false, fmt.Errorf("pack put output id: %w", err)
	}

	s.mu.RLock()
	prev, prevOK := s.byAction[actionID]
	existing, existOK := s.byOutput[outputID]
	s.mu.RUnlock()
	if prevOK {
		return prev, false, nil
	}

	// Re-checked under the append lock: abort the append if a racing Put
	// committed this action after the read above.
	pre := func() bool {
		s.mu.RLock()
		_, ok := s.byAction[actionID]
		s.mu.RUnlock()
		return !ok
	}

	if existOK && existing.dataLen == int64(len(data)) {
		loc := existing
		loc.created = packNow()
		_, aborted, err := s.appendRecordLoc(packAliasMagic, aid, oid, nil, pre, func(packLoc) {
			s.mu.Lock()
			s.byAction[actionID] = loc
			s.mu.Unlock()
		})
		if err != nil {
			return packLoc{}, false, err
		}
		if aborted {
			s.mu.RLock()
			cur := s.byAction[actionID]
			s.mu.RUnlock()
			return cur, false, nil
		}
		s.Stats.Puts.Increment()
		return loc, true, nil
	}

	var loc packLoc
	_, aborted, err := s.appendRecordLoc(packRecordMagic, aid, oid, data, pre, func(l packLoc) {
		loc = l
		s.verified.put(verifyKey{packID: l.packID, dataOff: l.dataOff}, verifyInfoForPut(outputID, data))
		s.mu.Lock()
		s.byAction[actionID] = l
		s.byOutput[outputID] = l
		s.mu.Unlock()
	})
	if err != nil {
		return packLoc{}, false, err
	}
	if aborted {
		s.mu.RLock()
		cur := s.byAction[actionID]
		s.mu.RUnlock()
		return cur, false, nil
	}
	s.Stats.Puts.Increment()
	return loc, true, nil
}

// appendRecordLoc appends a record (fixed header + optional body) and returns
// the body's location within the pack. The header and body are written
// separately rather than concatenated into one buffer, so a large body already
// held in memory (e.g. an archive fetched from the remote tier) is not copied
// again just to be written.
//
// pre, if non-nil, runs under the append lock BEFORE anything is written;
// returning false aborts the append (nothing is written, aborted is true).
// commit, if non-nil, runs under the append lock AFTER a successful write —
// index updates placed here are guaranteed to happen in pack-file record
// order, so a later startup scan replays them to the same final state the
// live index reached.
func (s *PackStore) appendRecordLoc(magic uint32, aid, oid, data []byte, pre func() bool, commit func(packLoc)) (packLoc, bool, error) {
	created := packNow()
	crc := crc32.Checksum(data, packCRC)
	var hdr [packHeaderLen]byte
	binary.LittleEndian.PutUint32(hdr[0:4], magic)
	copy(hdr[4:4+hashLen], aid)
	copy(hdr[4+hashLen:4+2*hashLen], oid)
	binary.LittleEndian.PutUint64(hdr[4+2*hashLen:12+2*hashLen], uint64(created))
	binary.LittleEndian.PutUint64(hdr[12+2*hashLen:20+2*hashLen], uint64(len(data)))
	binary.LittleEndian.PutUint32(hdr[20+2*hashLen:packHeaderLen], crc)

	var loc packLoc
	var rawCommit func(id int, off int64)
	if commit != nil {
		rawCommit = func(id int, off int64) {
			loc = packLoc{
				packID:   id,
				dataOff:  off + packHeaderLen,
				dataLen:  int64(len(data)),
				created:  created,
				outputID: hex.EncodeToString(oid),
				crc:      crc,
			}
			commit(loc)
		}
	}
	id, off, aborted, err := s.appendRaw(hdr[:], data, pre, rawCommit)
	if err != nil || aborted {
		return packLoc{}, aborted, err
	}
	if commit == nil {
		loc = packLoc{
			packID:   id,
			dataOff:  off + packHeaderLen,
			dataLen:  int64(len(data)),
			created:  created,
			outputID: hex.EncodeToString(oid),
			crc:      crc,
		}
	}
	return loc, false, nil
}

// appendRaw appends a header and optional body to the active pack under wmu and
// returns where the record landed, rotating to a new pack afterward if it's
// full. On a partial write the size is not advanced, so the orphaned header is
// overwritten by the next append (and read back as a torn record meanwhile).
//
// pre and commit run under wmu (see appendRecordLoc). Both may take s.mu; the
// lock order is always wmu -> mu, and no code path acquires wmu while holding
// mu, so this cannot deadlock.
func (s *PackStore) appendRaw(hdr, body []byte, pre func() bool, commit func(id int, off int64)) (id int, off int64, aborted bool, err error) {
	s.wmu.Lock()
	if pre != nil && !pre() {
		s.wmu.Unlock()
		return 0, 0, true, nil
	}
	id = s.activeID
	off = s.activeSize
	f := s.pack(id)
	if f == nil {
		s.wmu.Unlock()
		return 0, 0, false, fmt.Errorf("pack append: active pack %d missing", id)
	}
	if _, err := f.WriteAt(hdr, off); err != nil {
		s.wmu.Unlock()
		return 0, 0, false, fmt.Errorf("pack append header: %w", err)
	}
	if len(body) > 0 {
		if _, err := f.WriteAt(body, off+int64(len(hdr))); err != nil {
			s.wmu.Unlock()
			return 0, 0, false, fmt.Errorf("pack append body: %w", err)
		}
	}
	s.activeSize = off + int64(len(hdr)) + int64(len(body))
	if commit != nil {
		commit(id, off)
	}
	rotate := s.activeSize >= maxPackBytes
	s.wmu.Unlock()

	if rotate {
		s.wmu.Lock()
		if s.activeID == id { // not already rotated by another goroutine
			if err := s.openActive(id + 1); err != nil {
				// Pack keeps growing past maxPackBytes until a later rotation succeeds; warn rather than fail silently.
				logger.Warn("cacheprog: pack rotation to %d failed: %v (active pack keeps growing)", id+1, err)
			}
		}
		s.wmu.Unlock()
	}
	return id, off, false, nil
}
