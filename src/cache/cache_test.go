package cache

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wow-look-at-my/testify/require"
)

// memBackend is a simple in-memory IBackend for testing.
type memBackend struct {
	mu    sync.Mutex
	store map[string]memEntry
	stats CacheStats
}

type memEntry struct {
	outputID	string
	data		[]byte
	time		time.Time
}

func newMemBackend() *memBackend {
	return &memBackend{store: make(map[string]memEntry)}
}

func (m *memBackend) Get(actionID string) (string, io.ReadCloser, int64, time.Time, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.store[actionID]
	if !ok {
		return "", nil, 0, time.Time{}, true, nil
	}
	return e.outputID, io.NopCloser(bytes.NewReader(e.data)), int64(len(e.data)), e.time, false, nil
}

func (m *memBackend) Put(actionID, outputID string, body io.Reader, bodySize int64) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.store[actionID] = memEntry{outputID: outputID, data: data, time: time.Now()}
	m.mu.Unlock()
	return nil
}

func (m *memBackend) Close() error        { return nil }
func (m *memBackend) GetStats() *CacheStats { return &m.stats }

func TestServer_Handshake(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.Nil(t, err)

	// Send a close command immediately.
	input := makeRequest(Request{ID: 1, Command: CmdClose})

	var out bytes.Buffer
	srv := NewServer(lc, nil)
	require.NoError(t, srv.Run(strings.NewReader(input), &out))

	// Decode handshake.
	dec := json.NewDecoder(&out)
	var handshake Response
	require.NoError(t, dec.Decode(&handshake))

	require.Equal(t, 3, len(handshake.KnownCommands))

	// Decode close response.
	var closeResp Response
	require.NoError(t, dec.Decode(&closeResp))

	require.Equal(t, int64(1), closeResp.ID)

}

func TestServer_PutAndGet(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.Nil(t, err)

	actionID := []byte{0xaa, 0xbb, 0xcc, 0xdd, 0x00, 0x11, 0x22, 0x33, 0xaa, 0xbb, 0xcc, 0xdd, 0x00, 0x11, 0x22, 0x33}
	outputID := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	body := "test cache body data"

	// Build input: PUT, GET, CLOSE.
	var input strings.Builder
	input.WriteString(makePutRequest(Request{
		ID:		1,
		Command:	CmdPut,
		ActionID:	actionID,
		OutputID:	outputID,
		BodySize:	int64(len(body)),
	}, body))
	input.WriteString(makeRequest(Request{
		ID:		2,
		Command:	CmdGet,
		ActionID:	actionID,
	}))
	input.WriteString(makeRequest(Request{
		ID:		3,
		Command:	CmdClose,
	}))

	var out bytes.Buffer
	srv := NewServer(lc, nil)
	require.NoError(t, srv.Run(strings.NewReader(input.String()), &out))

	// Parse all responses.
	responses := parseResponses(t, out.Bytes())

	// Find GET response (ID=2).
	var getResp *Response
	for i := range responses {
		if responses[i].ID == 2 {
			getResp = &responses[i]
			break
		}
	}
	require.NotNil(t, getResp)

	require.False(t, getResp.Miss)

	require.NotEqual(t, "", getResp.DiskPath)

}

func TestServer_PutRawBase64(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.Nil(t, err)

	actionID := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00}
	outputID := []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99}
	body := "raw base64 test body"

	// Build input using raw base64 format (Go >=1.25): PUT, GET, CLOSE.
	var input strings.Builder
	input.WriteString(makePutRequestRawBase64(Request{
		ID:       1,
		Command:  CmdPut,
		ActionID: actionID,
		OutputID: outputID,
		BodySize: int64(len(body)),
	}, body))
	input.WriteString(makeRequest(Request{
		ID:       2,
		Command:  CmdGet,
		ActionID: actionID,
	}))
	input.WriteString(makeRequest(Request{
		ID:      3,
		Command: CmdClose,
	}))

	var out bytes.Buffer
	srv := NewServer(lc, nil)
	require.NoError(t, srv.Run(strings.NewReader(input.String()), &out))

	responses := parseResponses(t, out.Bytes())

	// Find GET response (ID=2).
	var getResp *Response
	for i := range responses {
		if responses[i].ID == 2 {
			getResp = &responses[i]
			break
		}
	}
	require.NotNil(t, getResp)
	require.False(t, getResp.Miss)
	require.NotEqual(t, "", getResp.DiskPath)

	// Verify the file on disk has the right content.
	data, err := os.ReadFile(getResp.DiskPath)
	require.NoError(t, err)
	require.Equal(t, body, string(data))
}

func TestServer_GetMiss(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.Nil(t, err)

	actionID := []byte{0xde, 0xad, 0xbe, 0xef, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

	var input strings.Builder
	input.WriteString(makeRequest(Request{
		ID:		1,
		Command:	CmdGet,
		ActionID:	actionID,
	}))
	input.WriteString(makeRequest(Request{
		ID:		2,
		Command:	CmdClose,
	}))

	var out bytes.Buffer
	srv := NewServer(lc, nil)
	require.NoError(t, srv.Run(strings.NewReader(input.String()), &out))

	responses := parseResponses(t, out.Bytes())
	var getResp *Response
	for i := range responses {
		if responses[i].ID == 1 {
			getResp = &responses[i]
			break
		}
	}
	require.NotNil(t, getResp)

	require.True(t, getResp.Miss)

}

func TestServer_WithRemoteBackend(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.Nil(t, err)

	backend := newMemBackend()

	actionID := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	outputID := []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11}
	body := "remote backend data"

	// Pre-populate the remote backend.
	backend.Put(fmt.Sprintf("%x", actionID), fmt.Sprintf("%x", outputID), strings.NewReader(body), int64(len(body)))

	// GET from server (local miss, remote hit).
	var input strings.Builder
	input.WriteString(makeRequest(Request{
		ID:		1,
		Command:	CmdGet,
		ActionID:	actionID,
	}))
	input.WriteString(makeRequest(Request{
		ID:		2,
		Command:	CmdClose,
	}))

	var out bytes.Buffer
	srv := NewServer(lc, backend)
	require.NoError(t, srv.Run(strings.NewReader(input.String()), &out))

	responses := parseResponses(t, out.Bytes())
	var getResp *Response
	for i := range responses {
		if responses[i].ID == 1 {
			getResp = &responses[i]
			break
		}
	}
	require.NotNil(t, getResp)

	require.False(t, getResp.Miss)

	require.NotEqual(t, "", getResp.DiskPath)

}

func TestServer_EOFWithoutClose(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.Nil(t, err)

	// Just send a GET, no close command. The server should handle EOF gracefully.
	actionID := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	input := makeRequest(Request{
		ID:		1,
		Command:	CmdGet,
		ActionID:	actionID,
	})

	var out bytes.Buffer
	srv := NewServer(lc, nil)
	require.NoError(t, srv.Run(strings.NewReader(input), &out))

}

func TestHexToBytes(t *testing.T) {
	b := hexToBytes("aabb")
	require.False(t, len(b) != 2 || b[0] != 0xaa || b[1] != 0xbb)

}

func TestHexToBytes_Empty(t *testing.T) {
	b := hexToBytes("")
	require.Equal(t, 0, len(b))

}

func TestServer_Lock(t *testing.T) {
	srv := NewServer(nil, nil)
	mu1 := srv.lock("key1")
	mu2 := srv.lock("key1")
	require.True(t, mu1 == mu2, "same key should return same mutex")

	mu3 := srv.lock("key2")
	require.True(t, mu1 != mu3, "different keys should return different mutexes")
}

func TestFileSize(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "test-*")
	require.NoError(t, err)
	f.WriteString("hello")
	f.Close()

	require.Equal(t, int64(5), fileSize(f.Name()))
}

func TestFileSize_Missing(t *testing.T) {
	require.Equal(t, int64(0), fileSize(filepath.Join(t.TempDir(), "does-not-exist")))
}

func TestServer_PutWithRemote(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.NoError(t, err)

	backend := newMemBackend()
	actionID := []byte{0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11, 0x22, 0x33, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11, 0x22, 0x33}
	outputID := []byte{0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb}
	body := "data for remote"

	var input strings.Builder
	input.WriteString(makePutRequest(Request{
		ID:       1,
		Command:  CmdPut,
		ActionID: actionID,
		OutputID: outputID,
		BodySize: int64(len(body)),
	}, body))
	input.WriteString(makeRequest(Request{
		ID:      2,
		Command: CmdClose,
	}))

	var out bytes.Buffer
	srv := NewServer(lc, backend)
	require.NoError(t, srv.Run(strings.NewReader(input.String()), &out))

	// Give async remote write time to complete.
	time.Sleep(50 * time.Millisecond)

	// Verify data was written to remote.
	_, _, _, _, miss, err := backend.Get(fmt.Sprintf("%x", actionID))
	require.NoError(t, err)
	require.False(t, miss)
}

func TestServer_PutDuplicate(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.NoError(t, err)

	actionID := []byte{0xaa, 0xbb, 0xcc, 0xdd, 0x00, 0x11, 0x22, 0x33, 0xaa, 0xbb, 0xcc, 0xdd, 0x00, 0x11, 0x22, 0x33}
	outputID := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	body := "duplicate data"

	// PUT same action twice, then CLOSE.
	var input strings.Builder
	input.WriteString(makePutRequest(Request{
		ID: 1, Command: CmdPut, ActionID: actionID, OutputID: outputID,
		BodySize: int64(len(body)),
	}, body))
	input.WriteString(makePutRequest(Request{
		ID: 2, Command: CmdPut, ActionID: actionID, OutputID: outputID,
		BodySize: int64(len(body)),
	}, body))
	input.WriteString(makeRequest(Request{ID: 3, Command: CmdClose}))

	var out bytes.Buffer
	srv := NewServer(lc, nil)
	require.NoError(t, srv.Run(strings.NewReader(input.String()), &out))

	responses := parseResponses(t, out.Bytes())
	// Should have handshake + 3 responses (2 puts + 1 close) = 4 total
	require.GreaterOrEqual(t, len(responses), 4)
}

// errBackend is an IBackend that always returns errors.
type errBackend struct{ stats CacheStats }

func (e *errBackend) Get(actionID string) (string, io.ReadCloser, int64, time.Time, bool, error) {
	return "", nil, 0, time.Time{}, false, fmt.Errorf("backend error")
}

func (e *errBackend) Put(actionID, outputID string, body io.Reader, bodySize int64) error {
	return fmt.Errorf("backend error")
}

func (e *errBackend) Close() error        { return nil }
func (e *errBackend) GetStats() *CacheStats { return &e.stats }

func TestServer_GetWithRemoteError(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.NoError(t, err)

	actionID := []byte{0xff, 0xee, 0xdd, 0xcc, 0xbb, 0xaa, 0x99, 0x88, 0xff, 0xee, 0xdd, 0xcc, 0xbb, 0xaa, 0x99, 0x88}

	var input strings.Builder
	input.WriteString(makeRequest(Request{ID: 1, Command: CmdGet, ActionID: actionID}))
	input.WriteString(makeRequest(Request{ID: 2, Command: CmdClose}))

	var out bytes.Buffer
	srv := NewServer(lc, &errBackend{})
	require.NoError(t, srv.Run(strings.NewReader(input.String()), &out))

	responses := parseResponses(t, out.Bytes())
	var getResp *Response
	for i := range responses {
		if responses[i].ID == 1 {
			getResp = &responses[i]
			break
		}
	}
	require.NotNil(t, getResp)
	require.True(t, getResp.Miss, "error from remote should result in miss")
}

func TestServer_Stats(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.Nil(t, err)

	backend := newMemBackend()
	srv := NewServer(lc, backend)

	actionID := []byte{0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89}
	outputID := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00}
	missID := []byte{0xff, 0xee, 0xdd, 0xcc, 0xbb, 0xaa, 0x99, 0x88, 0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11, 0x00}

	var input strings.Builder
	// PUT
	input.WriteString(makePutRequest(Request{
		ID: 1, Command: CmdPut, ActionID: actionID, OutputID: outputID, BodySize: 5,
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
	// local.Put was called once (the PUT), local.Get was called twice (the GET + the dedupe check in handlePut)
	require.Equal(t, uint32(1), stats.Local.Puts.Load())
	require.Equal(t, uint32(1), stats.Local.Hits.Load())
	require.Equal(t, uint32(1), stats.Misses.Load())
	require.NotNil(t, stats.Remote)
}

func TestServer_Latency(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.Nil(t, err)

	backend := newMemBackend()
	srv := NewServer(lc, backend)

	actionID := []byte{0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89}
	outputID := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00}
	missID := []byte{0xff, 0xee, 0xdd, 0xcc, 0xbb, 0xaa, 0x99, 0x88, 0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11, 0x00}

	// Pre-populate remote so we exercise the remote get path.
	backend.Put(fmt.Sprintf("%x", missID), fmt.Sprintf("%x", outputID), strings.NewReader("remote data"), 11)

	var input strings.Builder
	// PUT (exercises: lock wait, local get for dedup check, local put)
	input.WriteString(makePutRequest(Request{
		ID: 1, Command: CmdPut, ActionID: actionID, OutputID: outputID, BodySize: 5,
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

	// Lock wait should have 3 entries (PUT, GET, GET).
	require.Equal(t, uint64(3), snap.LockWait.Count)

	// Local get: 3 calls (dedup check in PUT, local hit GET, local miss GET).
	require.Equal(t, uint64(3), snap.LocalGet.Count)

	// Local put: 2 calls (PUT, write-through from remote hit).
	require.Equal(t, uint64(2), snap.LocalPut.Count)

	// Remote get: 1 call (the missID lookup that hits remote).
	require.Equal(t, uint64(1), snap.RemoteGet.Count)

	// All latencies should be non-zero.
	require.Greater(t, snap.LockWait.MinUs, uint64(0))
	require.Greater(t, snap.LocalGet.MinUs, uint64(0))
	require.Greater(t, snap.LocalPut.MinUs, uint64(0))
	require.Greater(t, snap.RemoteGet.MinUs, uint64(0))
}

func TestStatsStreaming(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "stats.sock")

	sl, err := NewStatsListener(sockPath)
	require.NoError(t, err)

	t.Setenv("GOCACHE_STATS_SOCK", sockPath)

	actionID := []byte{0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89}
	outputID := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00}
	missID := []byte{0xff, 0xee, 0xdd, 0xcc, 0xbb, 0xaa, 0x99, 0x88, 0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11, 0x00}

	// Run a server with PUT, GET (hit), GET (miss), CLOSE.
	lc, err := NewLocalCache(filepath.Join(dir, "cache"))
	require.NoError(t, err)

	var input strings.Builder
	input.WriteString(makePutRequest(Request{
		ID: 1, Command: CmdPut, ActionID: actionID, OutputID: outputID, BodySize: 5,
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
	sockPath := filepath.Join(dir, "stats.sock")

	sl, err := NewStatsListener(sockPath)
	require.NoError(t, err)

	t.Setenv("GOCACHE_STATS_SOCK", sockPath)

	actionID := []byte{0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89}
	outputID := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00}

	lc, err := NewLocalCache(filepath.Join(dir, "cache"))
	require.NoError(t, err)

	var input strings.Builder
	input.WriteString(makePutRequest(Request{
		ID: 1, Command: CmdPut, ActionID: actionID, OutputID: outputID, BodySize: 5,
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
	// PUT + GET = 2 lock waits
	require.Equal(t, uint64(2), snap.LockWait.Count)
	// dedup check in PUT + GET = 2 local gets
	require.Equal(t, uint64(2), snap.LocalGet.Count)
	// 1 local put from the PUT command
	require.Equal(t, uint64(1), snap.LocalPut.Count)
}

// slowBackend wraps a memBackend and adds artificial latency to Get calls.
type slowBackend struct {
	*memBackend
	getDelay time.Duration
}

func (s *slowBackend) Get(actionID string) (string, io.ReadCloser, int64, time.Time, bool, error) {
	time.Sleep(s.getDelay)
	return s.memBackend.Get(actionID)
}

func TestServer_ConcurrentGets(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.NoError(t, err)

	backend := &slowBackend{memBackend: newMemBackend(), getDelay: 50 * time.Millisecond}

	// Pre-populate remote with 5 different keys.
	const n = 5
	actionIDs := make([][]byte, n)
	outputID := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	for i := range n {
		id := make([]byte, 16)
		id[0] = byte(i + 1)
		actionIDs[i] = id
		backend.Put(fmt.Sprintf("%x", id), fmt.Sprintf("%x", outputID), strings.NewReader(fmt.Sprintf("data-%d", i)), 6)
	}

	// Build input: 5 GETs (all remote hits) + CLOSE.
	var input strings.Builder
	for i := range n {
		input.WriteString(makeRequest(Request{
			ID: int64(i + 1), Command: CmdGet, ActionID: actionIDs[i],
		}))
	}
	input.WriteString(makeRequest(Request{ID: int64(n + 1), Command: CmdClose}))

	var out bytes.Buffer
	srv := NewServer(lc, backend)

	start := time.Now()
	require.NoError(t, srv.Run(strings.NewReader(input.String()), &out))
	elapsed := time.Since(start)

	// All 5 responses should be present.
	responses := parseResponses(t, out.Bytes())
	var hits int
	for _, r := range responses {
		if r.ID >= 1 && r.ID <= int64(n) && !r.Miss {
			hits++
		}
	}
	require.Equal(t, n, hits, "all GETs should be remote hits")

	// If GETs were sequential: 5 × 50ms = 250ms minimum.
	// If concurrent: ~50ms (1 round of latency).
	// Allow generous slack but ensure it's well under sequential time.
	require.Less(t, elapsed, time.Duration(n)*backend.getDelay,
		"concurrent GETs should complete faster than sequential (got %v, sequential would be >%v)",
		elapsed, time.Duration(n)*backend.getDelay)
}

func TestServer_PutEmpty(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.NoError(t, err)

	actionID := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	outputID := []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11}

	// PUT with empty body (BodySize=0, no body line).
	var input strings.Builder
	input.WriteString(makeRequest(Request{
		ID: 1, Command: CmdPut, ActionID: actionID, OutputID: outputID, BodySize: 0,
	}))
	input.WriteString(makeRequest(Request{ID: 2, Command: CmdClose}))

	var out bytes.Buffer
	srv := NewServer(lc, nil)
	require.NoError(t, srv.Run(strings.NewReader(input.String()), &out))

	responses := parseResponses(t, out.Bytes())
	require.GreaterOrEqual(t, len(responses), 3)
}

// makeRequest serializes a request as a JSON line.
func makeRequest(req Request) string {
	b, _ := json.Marshal(req)
	return string(b) + "\n"
}

// makePutRequest serializes a PUT request with body as two JSON lines (Go <=1.24 format).
func makePutRequest(req Request, body string) string {
	header, _ := json.Marshal(req)
	bodyJSON, _ := json.Marshal(body)
	return string(header) + "\n" + string(bodyJSON) + "\n"
}

// makePutRequestRawBase64 serializes a PUT request with raw base64 body (Go >=1.25 format).
func makePutRequestRawBase64(req Request, body string) string {
	header, _ := json.Marshal(req)
	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	return string(header) + "\n" + encoded + "\n"
}

func parseResponses(t *testing.T, data []byte) []Response {
	t.Helper()
	var responses []Response
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		var resp Response
		if err := dec.Decode(&resp); err != nil {
			break
		}
		responses = append(responses, resp)
	}
	return responses
}
