package cache

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestServer_Stats(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.Nil(t, err)

	backend := newMemBackend()
	srv := NewServer(lc, backend)

	actionID := []byte{0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89}
	sum := sha256.Sum256([]byte("hello"))
	missID := []byte{0xff, 0xee, 0xdd, 0xcc, 0xbb, 0xaa, 0x99, 0x88, 0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11, 0x00}

	var input strings.Builder
	// PUT
	input.WriteString(makePutRequest(Request{
		ID: 1, Command: CmdPut, ActionID: actionID, OutputID: sum[:], BodySize: 5,
	}, "hello"))
	// GET (local hit)
	input.WriteString(makeRequest(Request{ID: 2, Command: CmdGet, ActionID: actionID}))
	// GET (miss)
	input.WriteString(makeRequest(Request{ID: 3, Command: CmdGet, ActionID: missID}))
	// CLOSE
	input.WriteString(makeRequest(Request{ID: 4, Command: CmdClose}))

	var out bytes.Buffer
	require.NoError(t, srv.Run(strings.NewReader(input.String()), &out))

	stats := srv.GetStats()
	// local.Put was called for the PUT; local.Get was called for the GET and for the dedupe check in handlePut
	require.Equal(t, uint32(1), stats.Local.Puts.Load())
	require.Equal(t, uint32(1), stats.Local.Hits.Load())
	require.Equal(t, uint32(1), stats.Misses.Load())
	require.NotNil(t, stats.Remote)
}

// TestServer_PutDedupDoesNotCountLocalHit: the dedup lookup inside handlePut
// serves an entry the caller just recomputed — counting it as a cache hit
// inflated the hit rate on warm rebuilds.
func TestServer_PutDedupDoesNotCountLocalHit(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.NoError(t, err)
	srv := NewServer(lc, nil)

	actionID := bytes.Repeat([]byte{0xd0}, 32)
	body := "dedup body"
	sum := sha256.Sum256([]byte(body))

	var input strings.Builder
	input.WriteString(makePutRequest(Request{
		ID: 1, Command: CmdPut, ActionID: actionID, OutputID: sum[:], BodySize: int64(len(body)),
	}, body))
	input.WriteString(makePutRequest(Request{ // dedup: local already has it
		ID: 2, Command: CmdPut, ActionID: actionID, OutputID: sum[:], BodySize: int64(len(body)),
	}, body))
	input.WriteString(makeRequest(Request{ID: 3, Command: CmdClose}))

	var out bytes.Buffer
	require.NoError(t, srv.Run(strings.NewReader(input.String()), &out))

	require.Equal(t, uint32(0), lc.Stats.Hits.Load(),
		"PUT dedup lookups must not count as local cache hits")
	require.Equal(t, uint32(1), lc.Stats.Puts.Load())
}

// TestGetBatch_ShutdownDoesNotHangQueuedWaiters: a getBatch waiter whose
// request was still buffered in batchReqCh when the coalescer shut down must
// degrade to a miss, never block forever on a reply that will never come.
func TestGetBatch_ShutdownDoesNotHangQueuedWaiters(t *testing.T) {
	// Bare backend with NO coalescer goroutine: simulates the request
	// sitting in the buffered channel when shutdown lands.
	b := &WebBackend{
		batchReqCh: make(chan batchReq, 4),
		batchStop:  make(chan struct{}),
		batchDone:  make(chan struct{}),
	}

	type result struct {
		miss bool
		err  error
	}
	done := make(chan result, 1)
	go func() {
		_, _, _, _, miss, err := b.getBatch("aabbccdd", "go-buildcache/v1aabbccdd")
		done <- result{miss: miss, err: err}
	}()

	// Wait until the request is enqueued, then shut down without draining.
	require.Eventually(t, func() bool { return len(b.batchReqCh) == 1 }, time.Second, time.Millisecond)
	close(b.batchStop)
	close(b.batchDone)

	select {
	case r := <-done:
		require.NoError(t, r.err)
		require.True(t, r.miss, "an undrained queued request must miss cleanly on shutdown")
	case <-time.After(2 * time.Second):
		t.Fatal("getBatch waiter hung after coalescer shutdown")
	}
}

func TestSetHasRemote(t *testing.T) {
	sockPath := testSocketPath(t, "stats.sock")

	sl, err := NewStatsListener(sockPath)
	require.NoError(t, err)
	defer sl.Close()

	// Before SetHasRemote: Stats().Remote should be nil.
	stats := sl.Stats()
	require.Nil(t, stats.Remote, "Remote should be nil before SetHasRemote")

	// After SetHasRemote: Stats().Remote should be non-nil, with empty counters.
	sl.SetHasRemote()
	stats = sl.Stats()
	require.NotNil(t, stats.Remote, "Remote should be non-nil after SetHasRemote")
	require.Equal(t, uint32(0), stats.Remote.Hits.Load())
	require.Equal(t, uint32(0), stats.Remote.Puts.Load())
}

func TestServer_Latency(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.Nil(t, err)

	backend := newMemBackend()
	srv := NewServer(lc, backend)

	actionID := []byte{0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89}
	sum := sha256.Sum256([]byte("hello"))
	missID := []byte{0xff, 0xee, 0xdd, 0xcc, 0xbb, 0xaa, 0x99, 0x88, 0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11, 0x00}

	// Pre-populate remote so we exercise the remote get path.
	remoteSum := sha256.Sum256([]byte("remote data"))
	backend.Put(fmt.Sprintf("%x", missID), fmt.Sprintf("%x", remoteSum[:]), strings.NewReader("remote data"), 11)

	var input strings.Builder
	// PUT (exercises: lock wait, local get for dedup check, local put)
	input.WriteString(makePutRequest(Request{
		ID: 1, Command: CmdPut, ActionID: actionID, OutputID: sum[:], BodySize: 5,
	}, "hello"))
	// GET local hit (exercises: lock wait, local get)
	input.WriteString(makeRequest(Request{ID: 2, Command: CmdGet, ActionID: actionID}))
	// GET remote hit (exercises: lock wait, local get miss, remote get, local put for write-through)
	input.WriteString(makeRequest(Request{ID: 3, Command: CmdGet, ActionID: missID}))
	// CLOSE
	input.WriteString(makeRequest(Request{ID: 4, Command: CmdClose}))

	var out bytes.Buffer
	require.NoError(t, srv.Run(strings.NewReader(input.String()), &out))

	snap := srv.Latency.Snapshot()

	// Lock wait should have an entry per request (PUT, GET, GET).
	require.Equal(t, uint64(3), snap.LockWait.Count)

	// Local get is called for the dedup check in PUT, the local hit GET and the local miss GET.
	require.Equal(t, uint64(3), snap.LocalGet.Count)

	// Local put is called for the PUT and for the write-through from the remote hit.
	require.Equal(t, uint64(2), snap.LocalPut.Count)

	// Remote get is called for the missID lookup that hits remote.
	require.Equal(t, uint64(1), snap.RemoteGet.Count)

	// Every latency should be recorded.
	require.Greater(t, snap.LockWait.MinUs, uint64(0))
	require.Greater(t, snap.LocalGet.MinUs, uint64(0))
	require.Greater(t, snap.LocalPut.MinUs, uint64(0))
	require.Greater(t, snap.RemoteGet.MinUs, uint64(0))
}

func TestStatsStreaming(t *testing.T) {
	dir := t.TempDir()
	sockPath := testSocketPath(t, "stats.sock")

	sl, err := NewStatsListener(sockPath)
	require.NoError(t, err)

	t.Setenv("GOCACHE_STATS_SOCK", sockPath)

	actionID := []byte{0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89}
	sum := sha256.Sum256([]byte("hello"))
	missID := []byte{0xff, 0xee, 0xdd, 0xcc, 0xbb, 0xaa, 0x99, 0x88, 0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11, 0x00}

	// Run a server with PUT, GET (hit), GET (miss), CLOSE.
	lc, err := NewLocalCache(filepath.Join(dir, "cache"))
	require.NoError(t, err)

	var input strings.Builder
	input.WriteString(makePutRequest(Request{
		ID: 1, Command: CmdPut, ActionID: actionID, OutputID: sum[:], BodySize: 5,
	}, "hello"))
	input.WriteString(makeRequest(Request{ID: 2, Command: CmdGet, ActionID: actionID}))
	input.WriteString(makeRequest(Request{ID: 3, Command: CmdGet, ActionID: missID}))
	input.WriteString(makeRequest(Request{ID: 4, Command: CmdClose}))

	var out bytes.Buffer
	srv := NewServer(lc, nil)
	require.NoError(t, srv.Run(strings.NewReader(input.String()), &out))

	sl.Close()
	got := sl.Stats()
	require.Equal(t, uint32(1), got.Local.Puts.Load())
	require.Equal(t, uint32(1), got.Local.Hits.Load())
	require.Equal(t, uint32(1), got.Misses.Load())
}

func TestStatsStreamingLatency(t *testing.T) {
	dir := t.TempDir()
	sockPath := testSocketPath(t, "stats.sock")

	sl, err := NewStatsListener(sockPath)
	require.NoError(t, err)

	t.Setenv("GOCACHE_STATS_SOCK", sockPath)

	actionID := []byte{0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89}
	sum := sha256.Sum256([]byte("hello"))

	lc, err := NewLocalCache(filepath.Join(dir, "cache"))
	require.NoError(t, err)

	var input strings.Builder
	input.WriteString(makePutRequest(Request{
		ID: 1, Command: CmdPut, ActionID: actionID, OutputID: sum[:], BodySize: 5,
	}, "hello"))
	input.WriteString(makeRequest(Request{ID: 2, Command: CmdGet, ActionID: actionID}))
	input.WriteString(makeRequest(Request{ID: 3, Command: CmdClose}))

	var out bytes.Buffer
	srv := NewServer(lc, nil)
	require.NoError(t, srv.Run(strings.NewReader(input.String()), &out))

	sl.Close()
	got := sl.Stats()
	require.NotNil(t, got.Latency)

	snap := got.Latency.Snapshot()
	// A lock wait for the PUT and for the GET
	require.Equal(t, uint64(2), snap.LockWait.Count)
	// A local get for the dedup check in PUT and for the GET
	require.Equal(t, uint64(2), snap.LocalGet.Count)
	// A local put from the PUT command
	require.Equal(t, uint64(1), snap.LocalPut.Count)
}
