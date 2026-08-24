package cache

import (
	"os"
	"sort"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// evictPacksToBudget bounds cross-build growth at startup: when the packs on
// disk total more than packResetBytes, whole packs are deleted OLDEST-FIRST
// (lowest id — pack ids grow monotonically, so the lowest id holds the oldest
// records) until the total is at or under ~80% of the budget, leaving
// headroom for this build's writes. The newest pack is never deleted: it
// becomes the append target and holds the most recently written — most
// likely still hot — records.
//
// This replaces the old wholesale reset, which deleted EVERY pack the moment
// the total crossed the budget: a working set >= packResetBytes cold-cycled
// forever, paying a full recompute on every build.
//
// Called from OpenPackStore before the surviving packs are opened/scanned
// (and before anything is served), so no index entries or memoized
// verifications reference the deleted packs. Returns the surviving ids.
func (s *PackStore) evictPacksToBudget(ids []int, total int64) []int {
	if total <= packResetBytes || len(ids) == 0 {
		return ids
	}
	target := packResetBytes / 10 * 8
	sort.Ints(ids)
	kept := ids
	var evicted int
	var freed int64
	for len(kept) > 1 && total > target { // never the newest (last) pack
		id := kept[0]
		path := s.packPath(id)
		var size int64
		if info, err := os.Stat(path); err == nil {
			size = info.Size()
		}
		if err := os.Remove(path); err != nil {
			// Cannot delete (permissions, races): stop rather than spin; the store still works, just over budget.
			logger.Warn("cacheprog: pack eviction: remove %s: %v", path, err)
			break
		}
		s.verified.dropPack(id)
		total -= size
		freed += size
		evicted++
		kept = kept[1:]
	}
	if evicted > 0 {
		logger.Info("cacheprog: pack cache over budget: evicted %d oldest pack(s), freed %d MiB; %d pack(s) kept",
			evicted, freed>>20, len(kept))
	}
	if total > packResetBytes {
		logger.Warn("cacheprog: pack cache still over budget after eviction (%d MiB; the newest pack is never evicted)", total>>20)
	}
	return kept
}
