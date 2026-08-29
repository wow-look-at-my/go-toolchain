package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Tests for the protocol read loop (readloop.go): wire-format acceptance
// (quoted and raw base64 bodies), the removal of the old Scanner cap,
// and strict body decoding (malformed bodies fail only that PUT; a stream
// truncated at EOF stores nothing).

func TestServer_PutRawBase64(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.Nil(t, err)

	actionID := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00}
	body := "raw base64 test body"
	sum := sha256.Sum256([]byte(body))

	// Build input using the raw (unquoted) base64 body form: PUT, GET, CLOSE.
	var input strings.Builder
	input.WriteString(makePutRequestRawBase64(Request{
		ID:       1,
		Command:  CmdPut,
		ActionID: actionID,
		OutputID: sum[:],
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

	// Find the GET response by its request ID.
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

func TestServer_PutBodyOver64MiB(t *testing.T) {
	// Regression: a PUT body line over the old bufio.Scanner cap must still round-trip byte-for-byte.
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.NoError(t, err)

	const bodySize = 70 << 20 // a raw body whose base64 line runs past the old cap
	body := make([]byte, bodySize)
	for i := range body {
		body[i] = byte((i * 31) >> 3)
	}
	sum := sha256.Sum256(body)
	actionID := bytes.Repeat([]byte{0x42}, 32)

	var input bytes.Buffer
	hdr, _ := json.Marshal(Request{
		ID: 1, Command: CmdPut, ActionID: actionID, OutputID: sum[:], BodySize: bodySize,
	})
	input.Write(hdr)
	input.WriteString("\n\n\"")
	enc := base64.NewEncoder(base64.StdEncoding, &input)
	_, err = enc.Write(body)
	require.NoError(t, err)
	require.NoError(t, enc.Close())
	input.WriteString("\"\n")
	input.WriteString(makeRequest(Request{ID: 2, Command: CmdGet, ActionID: actionID}))
	input.WriteString(makeRequest(Request{ID: 3, Command: CmdClose}))

	var out bytes.Buffer
	srv := NewServer(lc, nil)
	require.NoError(t, srv.Run(&input, &out))

	responses := parseResponses(t, out.Bytes())
	var putResp, getResp *Response
	for i := range responses {
		switch responses[i].ID {
		case 1:
			putResp = &responses[i]
		case 2:
			getResp = &responses[i]
		}
	}
	require.NotNil(t, putResp)
	require.Empty(t, putResp.Err, "an oversized PUT must succeed, not error")
	require.NotNil(t, getResp)
	require.False(t, getResp.Miss)
	require.Equal(t, int64(bodySize), getResp.Size)
	data, err := os.ReadFile(getResp.DiskPath)
	require.NoError(t, err)
	require.True(t, bytes.Equal(body, data), "stored body must round-trip byte-for-byte")
}

func TestServer_PutMalformedBodyLine(t *testing.T) {
	// A malformed body line must fail ONLY that PUT — Err reply, nothing
	// stored (never an empty body under the request's real IDs) — and leave
	// the protocol loop alive to serve subsequent requests.
	cases := []struct {
		name     string
		bodyLine string
		bodySize int64
	}{
		{"raw invalid base64", "not-base64-at-all!!!", 5},
		{"quoted invalid base64", `"####"`, 3},
		{"size mismatch", `"YWJj"`, 5}, // decodes to "abc", declared longer
		{"unterminated quote", `"YWJj`, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			lc, err := NewLocalCache(dir)
			require.NoError(t, err)

			badAction := bytes.Repeat([]byte{0xba}, 32)
			goodAction := bytes.Repeat([]byte{0x60}, 32)
			goodBody := "good body"
			goodSum := sha256.Sum256([]byte(goodBody))

			var input strings.Builder
			hdr, _ := json.Marshal(Request{
				ID: 1, Command: CmdPut, ActionID: badAction,
				OutputID: bytes.Repeat([]byte{0x01}, 32), BodySize: tc.bodySize,
			})
			input.WriteString(string(hdr) + "\n" + tc.bodyLine + "\n")
			input.WriteString(makeRequest(Request{ID: 2, Command: CmdGet, ActionID: badAction}))
			input.WriteString(makePutRequest(Request{
				ID: 3, Command: CmdPut, ActionID: goodAction, OutputID: goodSum[:],
				BodySize: int64(len(goodBody)),
			}, goodBody))
			input.WriteString(makeRequest(Request{ID: 4, Command: CmdGet, ActionID: goodAction}))
			input.WriteString(makeRequest(Request{ID: 5, Command: CmdClose}))

			var out bytes.Buffer
			srv := NewServer(lc, nil)
			require.NoError(t, srv.Run(strings.NewReader(input.String()), &out))

			resps := map[int64]Response{}
			for _, r := range parseResponses(t, out.Bytes()) {
				resps[r.ID] = r
			}
			require.NotEmpty(t, resps[1].Err, "malformed body must produce an Err reply")
			require.True(t, resps[2].Miss, "nothing may be stored for a malformed PUT")
			require.Empty(t, resps[3].Err, "the loop must keep serving after a malformed PUT")
			require.False(t, resps[4].Miss, "a later valid PUT must be stored and served")
			require.Equal(t, int64(5), resps[5].ID, "close must still be handled")
		})
	}
}

func TestServer_PutTruncatedAtEOF(t *testing.T) {
	// Input ends mid-PUT (header present, body missing); the loop must exit clean without storing anything.
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.NoError(t, err)

	actionID := bytes.Repeat([]byte{0x77}, 32)
	hdr, _ := json.Marshal(Request{
		ID: 1, Command: CmdPut, ActionID: actionID,
		OutputID: bytes.Repeat([]byte{0x02}, 32), BodySize: 1024,
	})

	var out bytes.Buffer
	srv := NewServer(lc, nil)
	require.NoError(t, srv.Run(strings.NewReader(string(hdr)+"\n"), &out))

	_, miss := lc.Get(fmt.Sprintf("%x", actionID))
	require.True(t, miss, "a truncated PUT must not store an empty body")
}

// makeRequest serializes a request as a JSON line.
func makeRequest(req Request) string {
	b, _ := json.Marshal(req)
	return string(b) + "\n"
}

// makePutRequest serializes a PUT the way cmd/go writes it: JSON header,
// blank line, then body as a quoted base64 string.
func makePutRequest(req Request, body string) string {
	header, _ := json.Marshal(req)
	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	return string(header) + "\n\n\"" + encoded + "\"\n"
}

// makePutRequestRawBase64 serializes a PUT request with a raw base64 body (the newer Go format).
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
