package cmd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wow-look-at-my/go-toolchain/src/cache"
)

func summaryLine(stats *cache.ServerStats) string {
	return strings.Join(cacheSummaryParts(stats), "  ")
}

// A build served off the shared tier hit the cache every time. Misses rises
// only when both tiers miss, so a rate over local hits and misses alone
// counts those lookups nowhere.
func TestHitRateCountsRemoteHits(t *testing.T) {
	sl := &cache.StatsListener{}
	sl.SetHasRemote()
	ss := sl.Stats()
	ss.Remote.Hits.Add(40)

	assert.Contains(t, summaryLine(ss), "(100% hit)")
}

func TestHitRateMixesBothTiers(t *testing.T) {
	sl := &cache.StatsListener{}
	sl.SetHasRemote()
	ss := sl.Stats()
	ss.Local.Hits.Add(30)
	ss.Remote.Hits.Add(60)
	ss.Misses.Add(10)

	assert.Contains(t, summaryLine(ss), "(90% hit)")
}

// With no remote configured the line is what it always was.
func TestHitRateLocalOnly(t *testing.T) {
	sl := &cache.StatsListener{}
	ss := sl.Stats()
	ss.Local.Hits.Add(3)
	ss.Misses.Add(1)

	line := summaryLine(ss)
	assert.Contains(t, line, "(75% hit)")
	assert.NotContains(t, line, "prefetched")
}

// The populated count alone cannot distinguish a prefetch of what the build
// went on to read from a prefetch of the wrong neighbours.
func TestPrefetchReportsTheShareUsed(t *testing.T) {
	sl := &cache.StatsListener{}
	sl.SetHasBatch()
	ss := sl.Stats()
	ss.Batch.Populated.Add(200)
	ss.Batch.Used.Add(50)

	assert.Contains(t, summaryLine(ss), "prefetched 200 (50 used, 25%)")
}

// Nothing prefetched: no prefetch clause, and no division by an empty count.
func TestPrefetchSilentWhenNothingPopulated(t *testing.T) {
	sl := &cache.StatsListener{}
	sl.SetHasBatch()
	ss := sl.Stats()

	assert.NotContains(t, summaryLine(ss), "prefetched")
}
