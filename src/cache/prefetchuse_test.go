package cache

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Taking an entry consumes it, so repeated reads cannot push the used count
// past the populated count.
func TestPrefetchSetConsumesTheEntry(t *testing.T) {
	p := newPrefetchSet()
	p.add("abc")

	assert.True(t, p.take("abc"), "reading a prefetched entry is a use")
	assert.False(t, p.take("abc"), "re-reading the same entry is not another use")
	assert.False(t, p.take("never-prefetched"))
}

// A nil set answers for every caller that has no prefetch wired.
func TestPrefetchSetNilIsInert(t *testing.T) {
	var p *prefetchSet
	p.add("abc")
	assert.False(t, p.take("abc"))
}

// A GET that reads back what the batch callback stored is a use; a GET for a
// key the build put here itself is not.
func TestLocalHitOnPrefetchedEntryCountsAsUse(t *testing.T) {
	local, err := NewLocalCache(t.TempDir())
	require.NoError(t, err)
	defer local.Close()

	srv := NewServer(local, nil)
	wb := newBareBackend("go-buildcache/")
	wireBatchCallbacks(wb, local, srv)

	body := "prefetched body"
	compressed, _ := compressData([]byte(body))
	prefetchedID := strings.Repeat("a", 64)
	wb.OnBatchEntries([]BatchEntry{
		{Key: "go-buildcache/v1" + prefetchedID, OutputID: testOutputID(body), Data: compressed, Prefetch: true},
	})
	require.Equal(t, uint32(1), srv.batch.Populated.Load())

	// A key this build stored itself, so a hit on it owes the prefetch nothing.
	localID := strings.Repeat("b", 64)
	_, err = local.Put(localID, testOutputID(body), strings.NewReader(body))
	require.NoError(t, err)

	srv.handleGet(Request{ID: 1, ActionID: hexToBytes(localID)})
	assert.Equal(t, uint32(0), srv.batch.Used.Load(), "a hit on a locally stored entry is not a prefetch use")

	resp := srv.handleGet(Request{ID: 2, ActionID: hexToBytes(prefetchedID)})
	require.False(t, resp.Miss, "the prefetched entry must be served from the local tier")
	assert.Equal(t, uint32(1), srv.batch.Used.Load())

	srv.handleGet(Request{ID: 3, ActionID: hexToBytes(prefetchedID)})
	assert.Equal(t, uint32(1), srv.batch.Used.Load(), "re-reading an entry is not another use")
}
