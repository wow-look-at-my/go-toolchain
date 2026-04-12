package cache

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wow-look-at-my/testify/require"
)

func TestWebBackend_SmallEntryBatched(t *testing.T) {
	// Small entries should be buffered, not uploaded individually.
	var putPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			putPaths = append(putPaths, r.URL.Path)
			io.ReadAll(r.Body)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)

	// Put a small entry (< 64KB).
	err = b.Put("aabb000011112222", "ccdd333344445555", nopReader("small data"), 10)
	require.NoError(t, err)

	// No individual PUT should have been made yet.
	for _, p := range putPaths {
		require.NotContains(t, p, "v1aabb000011112222",
			"small entry should not be uploaded individually")
	}

	// The entry should be in the batch buffer.
	b.batchMu.Lock()
	require.Len(t, b.batchBuf, 1)
	require.Equal(t, "aabb000011112222", b.batchBuf[0].actionID)
	b.batchMu.Unlock()
}

func TestWebBackend_LargeEntryIndividual(t *testing.T) {
	// Entries >= 64KB should be uploaded individually, not batched.
	var gotPutPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			gotPutPath = r.URL.Path
			io.ReadAll(r.Body)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)

	// Create a 65KB payload.
	large := make([]byte, 65*1024)
	for i := range large {
		large[i] = byte(i % 256)
	}

	err = b.Put("aabb000011112222", "ccdd333344445555", strings.NewReader(string(large)), int64(len(large)))
	require.NoError(t, err)

	// Should have been uploaded individually.
	require.Equal(t, "/testbucket/go-buildcache/v1aabb000011112222", gotPutPath)
	require.Equal(t, uint32(1), b.Stats.Puts.Load())

	// Batch buffer should be empty.
	b.batchMu.Lock()
	require.Empty(t, b.batchBuf)
	b.batchMu.Unlock()
}

func TestWebBackend_BatchFlushOnClose(t *testing.T) {
	// Closing the backend should flush buffered entries as a batch archive.
	store := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "PUT":
			body, _ := io.ReadAll(r.Body)
			store[r.URL.Path] = body
		case "GET":
			data, ok := store[r.URL.Path]
			if !ok {
				w.WriteHeader(404)
				return
			}
			w.Write(data)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)

	// Put several small entries.
	err = b.Put("aa00000000000001", "bb00000000000001", nopReader("entry one"), 9)
	require.NoError(t, err)
	err = b.Put("aa00000000000002", "bb00000000000002", nopReader("entry two"), 9)
	require.NoError(t, err)
	err = b.Put("aa00000000000003", "bb00000000000003", nopReader("entry three"), 11)
	require.NoError(t, err)

	// No puts counted yet (still buffered).
	require.Equal(t, uint32(0), b.Stats.Puts.Load())

	// Close triggers flush.
	require.NoError(t, b.Close())

	// Puts should now be counted.
	require.Equal(t, uint32(3), b.Stats.Puts.Load())

	// A batch archive should have been uploaded.
	var batchPath string
	for path := range store {
		if strings.Contains(path, "/batches/batch-") {
			batchPath = path
			break
		}
	}
	require.NotEmpty(t, batchPath, "batch archive should have been uploaded")

	// An index should have been uploaded.
	var indexPath string
	for path := range store {
		if strings.Contains(path, "/batches/index-json") {
			indexPath = path
			break
		}
	}
	require.NotEmpty(t, indexPath, "batch index should have been uploaded")

	// The batch index should contain all three entries.
	b.batchIndexMu.RLock()
	require.Len(t, b.batchIndex, 3)
	require.Contains(t, b.batchIndex, "aa00000000000001")
	require.Contains(t, b.batchIndex, "aa00000000000002")
	require.Contains(t, b.batchIndex, "aa00000000000003")
	b.batchIndexMu.RUnlock()
}

func TestWebBackend_BatchGetRoundTrip(t *testing.T) {
	// Put small entries, flush, then GET them back.
	store := map[string][]byte{}
	headers := map[string]http.Header{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "PUT":
			body, _ := io.ReadAll(r.Body)
			store[r.URL.Path] = body
			headers[r.URL.Path] = r.Header.Clone()
			w.WriteHeader(200)
		case "GET":
			data, ok := store[r.URL.Path]
			if !ok {
				w.WriteHeader(404)
				return
			}
			if h, ok := headers[r.URL.Path]; ok {
				for k, v := range h {
					for _, vv := range v {
						w.Header().Add(k, vv)
					}
				}
			}
			w.WriteHeader(200)
			w.Write(data)
		}
	}))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)

	// Put small entries and flush.
	b.Put("aa11111111111111", "bb11111111111111", nopReader("data one"), 8)
	b.Put("aa22222222222222", "bb22222222222222", nopReader("data two"), 8)
	b.Close()

	// Create a fresh backend (simulating a new session) that loads the batch index.
	b2, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)

	// The batch index should have been loaded from remote.
	require.Len(t, b2.batchIndex, 2)

	// GET should find entries in the batch.
	outputID, body, size, _, miss, err := b2.Get("aa11111111111111")
	require.NoError(t, err)
	require.False(t, miss)
	require.Equal(t, "bb11111111111111", outputID)
	data, _ := io.ReadAll(body)
	require.Equal(t, "data one", string(data))
	require.Equal(t, int64(8), size)

	outputID2, body2, _, _, miss2, err := b2.Get("aa22222222222222")
	require.NoError(t, err)
	require.False(t, miss2)
	require.Equal(t, "bb22222222222222", outputID2)
	data2, _ := io.ReadAll(body2)
	require.Equal(t, "data two", string(data2))
}

func TestWebBackend_BatchFlushByCount(t *testing.T) {
	// When batchFlushCount entries accumulate, flush should trigger.
	var batchUploaded bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" && strings.Contains(r.URL.Path, "/batches/batch-") {
			batchUploaded = true
			io.ReadAll(r.Body)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)

	// Put exactly batchFlushCount small entries.
	for i := 0; i < batchFlushCount; i++ {
		aid := fmt.Sprintf("%016x", i)
		oid := fmt.Sprintf("%016x", i+1000)
		b.Put(aid, oid, nopReader("x"), 1)
	}

	// The flush should have been triggered by the count threshold.
	require.True(t, batchUploaded, "batch should have been flushed at count threshold")
	require.Equal(t, uint32(batchFlushCount), b.Stats.Puts.Load())
}

func TestWebBackend_BatchGetPopulatesLocal(t *testing.T) {
	// When a batch is downloaded for one entry, the OnBatchEntries callback
	// should fire with ALL entries so the local cache can be populated.
	store := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "PUT":
			body, _ := io.ReadAll(r.Body)
			store[r.URL.Path] = body
			w.WriteHeader(200)
		case "GET":
			data, ok := store[r.URL.Path]
			if !ok {
				w.WriteHeader(404)
				return
			}
			w.WriteHeader(200)
			w.Write(data)
		}
	}))
	defer srv.Close()

	// Upload a batch with 3 entries.
	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)

	b.Put("pop1111111111111", "out1111111111111", nopReader("data-one"), 8)
	b.Put("pop2222222222222", "out2222222222222", nopReader("data-two"), 8)
	b.Put("pop3333333333333", "out3333333333333", nopReader("data-three"), 10)
	b.Close()

	// Create a new backend with OnBatchEntries callback.
	b2, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)

	var populated []extractedEntry
	b2.OnBatchEntries = func(entries []extractedEntry) {
		populated = append(populated, entries...)
	}

	// GET one entry — should trigger batch download and callback.
	outputID, body, _, _, miss, err := b2.Get("pop1111111111111")
	require.NoError(t, err)
	require.False(t, miss)
	require.Equal(t, "out1111111111111", outputID)
	data, _ := io.ReadAll(body)
	require.Equal(t, "data-one", string(data))

	// The callback should have received ALL 3 entries.
	require.Len(t, populated, 3)
	ids := map[string]bool{}
	for _, e := range populated {
		ids[e.ActionID] = true
	}
	require.True(t, ids["pop1111111111111"])
	require.True(t, ids["pop2222222222222"])
	require.True(t, ids["pop3333333333333"])
}

func TestWebBackend_BatchDedup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)

	// Put same actionID twice.
	b.Put("dedup00000000001", "out0000000000001", nopReader("first"), 5)
	b.Put("dedup00000000001", "out0000000000001", nopReader("first"), 5)

	// Only one entry should be buffered.
	b.batchMu.Lock()
	require.Len(t, b.batchBuf, 1)
	b.batchMu.Unlock()
}
