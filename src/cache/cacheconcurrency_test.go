package cache

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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
	t.Parallel()
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.NoError(t, err)

	backend := &slowBackend{memBackend: newMemBackend(), getDelay: 50 * time.Millisecond}

	// Pre-populate remote with a handful of different keys.
	const n = 5
	actionIDs := make([][]byte, n)
	outputID := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	for i := range n {
		id := make([]byte, 16)
		id[0] = byte(i + 1)
		actionIDs[i] = id
		backend.Put(fmt.Sprintf("%x", id), fmt.Sprintf("%x", outputID), strings.NewReader(fmt.Sprintf("data-%d", i)), 6)
	}

	// Build input: a GET per key (all remote hits), then CLOSE.
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

	// Every response should be present.
	responses := parseResponses(t, out.Bytes())
	var hits int
	for _, r := range responses {
		if r.ID >= 1 && r.ID <= int64(n) && !r.Miss {
			hits++
		}
	}
	require.Equal(t, n, hits, "all GETs should be remote hits")

	// Sequential GETs would cost a full server delay each; concurrent GETs
	// cost a single round of latency.
	// Allow generous slack but ensure it's well under sequential time.
	require.Less(t, elapsed, time.Duration(n)*backend.getDelay,
		"concurrent GETs should complete faster than sequential (got %v, sequential would be >%v)",
		elapsed, time.Duration(n)*backend.getDelay)
}

func TestServer_PutEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.NoError(t, err)

	actionID := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	outputID := []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11}

	// PUT with an empty body (no BodySize, no body line).
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
