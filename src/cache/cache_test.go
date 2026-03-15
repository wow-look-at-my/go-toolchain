package cache

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wow-look-at-my/testify/require"
)

// memBackend is a simple in-memory Backend for testing.
type memBackend struct {
	store map[string]memEntry
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
	m.store[actionID] = memEntry{outputID: outputID, data: data, time: time.Now()}
	return nil
}

func (m *memBackend) Close() error	{ return nil }

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

// makeRequest serializes a request as a JSON line.
func makeRequest(req Request) string {
	b, _ := json.Marshal(req)
	return string(b) + "\n"
}

// makePutRequest serializes a PUT request with body as two JSON lines.
func makePutRequest(req Request, body string) string {
	header, _ := json.Marshal(req)
	bodyJSON, _ := json.Marshal(body)
	return string(header) + "\n" + string(bodyJSON) + "\n"
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
