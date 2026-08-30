package cache

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDaemon_LatencyWiredOnceOnSharedBackend guards the daemon-mode data
// race: every connection's NewServer used to re-point the shared WebBackend's
// Latency sink at its own tracker — an unsynchronized write racing all other
// connections' in-flight web operations — and each connection's close then
// re-reported the shared cumulative pool snapshot, which the stats listener
// merges additively (an N-fold overcount for N connections). The sink must be
// wired a single time, by the daemon, and per-connection Servers over the
// no-close wrapper must leave it alone.
func TestDaemon_LatencyWiredOnceOnSharedBackend(t *testing.T) {
	setTempDir(t, t.TempDir())
	t.Setenv("GOCACHE_STATS_SOCK", "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	wb, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "k", SecretKey: "s",
	})
	require.NoError(t, err)

	dir := t.TempDir()
	lc, err := NewLocalCache(filepath.Join(dir, "cache"))
	require.NoError(t, err)

	d, err := NewDaemon(testSocketPath(t, "daemon.sock"), lc, wb)
	require.NoError(t, err)
	defer d.Close()

	require.Same(t, &d.latency, wb.Latency, "the daemon must wire the shared Latency sink once")

	// A per-connection Server must not re-point the shared sink at its own tracker.
	s := NewServer(lc, d.wrapped)
	require.NotNil(t, s)
	require.Same(t, &d.latency, wb.Latency, "per-connection Servers must not rewire the shared Latency sink")
	require.NotSame(t, &s.Latency, wb.Latency)
}
