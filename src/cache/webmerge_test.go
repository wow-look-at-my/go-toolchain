package cache

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeWebSummary_AddsCountersAndKeepsTheLargestIndex(t *testing.T) {
	t.Parallel()
	var dst WebSummary
	MergeWebSummary(&dst, WebSummary{Hits: 3, Puts: 1, MissNotInIndex: 5, SkippedNotInIndex: 5, IndexKeys: 900, IndexAuthoritative: true}, true)
	MergeWebSummary(&dst, WebSummary{Hits: 4, MissChecksum: 2, IndexKeys: 1200, IndexAuthoritative: true}, false)

	assert.Equal(t, uint32(7), dst.Hits)
	assert.Equal(t, uint32(1), dst.Puts)
	assert.Equal(t, uint32(5), dst.MissNotInIndex)
	assert.Equal(t, uint32(5), dst.SkippedNotInIndex)
	assert.Equal(t, uint32(2), dst.MissChecksum)
	assert.Equal(t, 1200, dst.IndexKeys, "one index offered to every process: the largest view of it, never the sum")
	assert.True(t, dst.IndexAuthoritative)
}

// A reporter that fell back leaves the run without a trustworthy view of the
// remote, whichever order the summaries arrive in.
func TestMergeWebSummary_AuthoritativeNeedsEveryReporter(t *testing.T) {
	t.Parallel()
	var late WebSummary
	MergeWebSummary(&late, WebSummary{IndexAuthoritative: true}, true)
	MergeWebSummary(&late, WebSummary{IndexAuthoritative: false}, false)
	assert.False(t, late.IndexAuthoritative)

	var early WebSummary
	MergeWebSummary(&early, WebSummary{IndexAuthoritative: false}, true)
	MergeWebSummary(&early, WebSummary{IndexAuthoritative: true}, false)
	assert.False(t, early.IndexAuthoritative, "the first summary must not be able to hide a later fallback")
}

func TestStatsListener_WebSummaryIsNilUntilStandaloneReports(t *testing.T) {
	t.Parallel()
	sl := &StatsListener{}
	assert.Nil(t, sl.WebSummary(), "no report means no web tier to speak for, not a zeroed one")

	sl.recordWeb(WebSummary{Hits: 2, IndexKeys: 10, IndexAuthoritative: true})
	sl.recordWeb(WebSummary{Hits: 5, IndexKeys: 10, IndexAuthoritative: true})

	got := sl.WebSummary()
	require.NotNil(t, got)
	assert.Equal(t, uint32(7), got.Hits)
	assert.Equal(t, 10, got.IndexKeys)
}

// The load-bearing half: a standalone cacheprog is the only process that knows
// what the remote did for it, so its close must carry those numbers to the
// parent. Without this a run whose every phase is namespaced reports a live
// remote as a dead one.
func TestFlushLatency_CarriesTheWebTierFromAStandaloneServer(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer remote.Close()

	web, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: remote.URL,
		AccessKey: "testkey", SecretKey: "testsecret",
	})
	require.NoError(t, err)
	defer web.Close()
	web.Stats.Hits.Add(6)
	web.MissNotInIndex.Add(2)

	sl, sock := newTestStatsListener(t)
	t.Setenv("GOCACHE_STATS_SOCK", sock)

	srv := NewServer(newTestLocalCache(t), web)
	srv.flushLatency()
	srv.closeStats()
	sl.Close()

	got := sl.WebSummary()
	require.NotNil(t, got, "a standalone server must report its web tier")
	assert.Equal(t, uint32(6), got.Hits)
	assert.Equal(t, uint32(2), got.MissNotInIndex)
}

// The daemon owns its own counters and reports them directly, so a
// per-connection Server must stay silent or the run counts them twice.
func TestFlushLatency_SaysNothingAboutTheDaemonsWebTier(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer remote.Close()

	web, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: remote.URL,
		AccessKey: "testkey", SecretKey: "testsecret",
	})
	require.NoError(t, err)
	defer web.Close()
	web.Stats.Hits.Add(6)

	sl, sock := newTestStatsListener(t)
	t.Setenv("GOCACHE_STATS_SOCK", sock)

	srv := NewServer(newTestLocalCache(t), &noCloseBackend{web})
	srv.flushLatency()
	srv.closeStats()
	sl.Close()

	assert.Nil(t, sl.WebSummary(), "a daemon connection reports no web summary of its own")
}

func newTestStatsListener(t *testing.T) (*StatsListener, string) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "stats.sock")
	sl, err := NewStatsListener(sock)
	require.NoError(t, err)
	return sl, sock
}

func newTestLocalCache(t *testing.T) LocalStore {
	t.Helper()
	local, err := NewLocalCache(t.TempDir())
	require.NoError(t, err)
	return local
}
