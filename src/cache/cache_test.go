package cache

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// memBackend is a simple in-memory Backend for testing.
type memBackend struct {
	store map[string]memEntry
}

type memEntry struct {
	outputID string
	data     []byte
	time     time.Time
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

func (m *memBackend) Close() error { return nil }

func TestServer_Handshake(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Send a close command immediately.
	input := makeRequest(Request{ID: 1, Command: CmdClose})

	var out bytes.Buffer
	srv := NewServer(lc, nil)
	if err := srv.Run(strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}

	// Decode handshake.
	dec := json.NewDecoder(&out)
	var handshake Response
	if err := dec.Decode(&handshake); err != nil {
		t.Fatal(err)
	}
	if len(handshake.KnownCommands) != 3 {
		t.Fatalf("expected 3 commands, got %d", len(handshake.KnownCommands))
	}

	// Decode close response.
	var closeResp Response
	if err := dec.Decode(&closeResp); err != nil {
		t.Fatal(err)
	}
	if closeResp.ID != 1 {
		t.Fatalf("expected ID=1, got %d", closeResp.ID)
	}
}

func TestServer_PutAndGet(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	if err != nil {
		t.Fatal(err)
	}

	actionID := []byte{0xaa, 0xbb, 0xcc, 0xdd, 0x00, 0x11, 0x22, 0x33, 0xaa, 0xbb, 0xcc, 0xdd, 0x00, 0x11, 0x22, 0x33}
	outputID := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	body := "test cache body data"

	// Build input: PUT, GET, CLOSE.
	var input strings.Builder
	input.WriteString(makePutRequest(Request{
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
	if err := srv.Run(strings.NewReader(input.String()), &out); err != nil {
		t.Fatal(err)
	}

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
	if getResp == nil {
		t.Fatal("no GET response found")
	}
	if getResp.Miss {
		t.Fatal("expected cache hit")
	}
	if getResp.DiskPath == "" {
		t.Fatal("expected non-empty DiskPath")
	}
}

func TestServer_GetMiss(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	if err != nil {
		t.Fatal(err)
	}

	actionID := []byte{0xde, 0xad, 0xbe, 0xef, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

	var input strings.Builder
	input.WriteString(makeRequest(Request{
		ID:       1,
		Command:  CmdGet,
		ActionID: actionID,
	}))
	input.WriteString(makeRequest(Request{
		ID:      2,
		Command: CmdClose,
	}))

	var out bytes.Buffer
	srv := NewServer(lc, nil)
	if err := srv.Run(strings.NewReader(input.String()), &out); err != nil {
		t.Fatal(err)
	}

	responses := parseResponses(t, out.Bytes())
	var getResp *Response
	for i := range responses {
		if responses[i].ID == 1 {
			getResp = &responses[i]
			break
		}
	}
	if getResp == nil {
		t.Fatal("no GET response found")
	}
	if !getResp.Miss {
		t.Fatal("expected cache miss")
	}
}

func TestServer_WithRemoteBackend(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	if err != nil {
		t.Fatal(err)
	}

	backend := newMemBackend()

	actionID := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	outputID := []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11}
	body := "remote backend data"

	// Pre-populate the remote backend.
	backend.Put(fmt.Sprintf("%x", actionID), fmt.Sprintf("%x", outputID), strings.NewReader(body), int64(len(body)))

	// GET from server (local miss, remote hit).
	var input strings.Builder
	input.WriteString(makeRequest(Request{
		ID:       1,
		Command:  CmdGet,
		ActionID: actionID,
	}))
	input.WriteString(makeRequest(Request{
		ID:      2,
		Command: CmdClose,
	}))

	var out bytes.Buffer
	srv := NewServer(lc, backend)
	if err := srv.Run(strings.NewReader(input.String()), &out); err != nil {
		t.Fatal(err)
	}

	responses := parseResponses(t, out.Bytes())
	var getResp *Response
	for i := range responses {
		if responses[i].ID == 1 {
			getResp = &responses[i]
			break
		}
	}
	if getResp == nil {
		t.Fatal("no GET response found")
	}
	if getResp.Miss {
		t.Fatal("expected cache hit from remote")
	}
	if getResp.DiskPath == "" {
		t.Fatal("expected non-empty DiskPath")
	}
}

func TestServer_EOFWithoutClose(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Just send a GET, no close command. The server should handle EOF gracefully.
	actionID := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	input := makeRequest(Request{
		ID:       1,
		Command:  CmdGet,
		ActionID: actionID,
	})

	var out bytes.Buffer
	srv := NewServer(lc, nil)
	if err := srv.Run(strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
}

func TestHexToBytes(t *testing.T) {
	b := hexToBytes("aabb")
	if len(b) != 2 || b[0] != 0xaa || b[1] != 0xbb {
		t.Fatalf("unexpected: %x", b)
	}
}

func TestHexToBytes_Empty(t *testing.T) {
	b := hexToBytes("")
	if len(b) != 0 {
		t.Fatalf("expected empty, got %x", b)
	}
}

func TestServer_Lock(t *testing.T) {
	srv := NewServer(nil, nil)
	mu1 := srv.lock("key1")
	mu2 := srv.lock("key1")
	if mu1 != mu2 {
		t.Fatal("expected same mutex for same key")
	}
	mu3 := srv.lock("key2")
	if mu1 == mu3 {
		t.Fatal("expected different mutex for different key")
	}
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
