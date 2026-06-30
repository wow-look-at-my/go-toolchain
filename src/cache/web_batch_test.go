package cache

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-containers/set"
)

// fakeBatchServer returns an httptest.Server that mimics the /_batch/get
// endpoint. store maps cache keys → compressed data. meta maps keys → metadata.
func fakeBatchServer(t *testing.T, store map[string][]byte, meta map[string]map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Individual PUT: store the object.
		if r.Method == "PUT" {
			key := r.URL.Path[len("/testbucket/"):]
			body, _ := io.ReadAll(r.Body)
			store[key] = body
			m := make(map[string]string)
			for k, v := range r.Header {
				lk := strings.ToLower(k)
				switch {
				case strings.HasPrefix(lk, "x-cache-meta-"):
					m[strings.TrimPrefix(lk, "x-cache-meta-")] = v[0]
				case strings.HasPrefix(lk, "x-amz-meta-"):
					// Deprecated alias; native wins if both present.
					mk := strings.TrimPrefix(lk, "x-amz-meta-")
					if _, ok := m[mk]; !ok {
						m[mk] = v[0]
					}
				}
			}
			meta[key] = m
			w.WriteHeader(200)
			return
		}

		// Batch GET endpoint.
		if r.Method == "GET" && r.URL.Path == "/testbucket/_batch/get" {
			var req batchGetRequest
			json.NewDecoder(r.Body).Decode(&req)

			var entries []batchGetManifestEntry
			dataMap := map[string][]byte{}
			for _, key := range req.Keys {
				d, ok := store[key]
				if !ok {
					continue
				}
				entries = append(entries, batchGetManifestEntry{
					Key:      key,
					Size:     int64(len(d)),
					Metadata: meta[key],
				})
				dataMap[key] = d
			}
			// Add a prefetch entry if requested and there are extra entries.
			if req.Prefetch {
				for key, d := range store {
					alreadyIncluded := false
					for _, e := range entries {
						if e.Key == key {
							alreadyIncluded = true
							break
						}
					}
					if !alreadyIncluded {
						entries = append(entries, batchGetManifestEntry{
							Key:      key,
							Size:     int64(len(d)),
							Metadata: meta[key],
							Prefetch: true,
						})
						dataMap[key] = d
					}
				}
			}

			manifest := batchGetManifest{Entries: entries}
			w.Header().Set("Content-Type", "application/x-tar")
			w.WriteHeader(200)

			tw := tar.NewWriter(w)
			mdata, _ := json.Marshal(manifest)
			tw.WriteHeader(&tar.Header{Name: "manifest.json", Size: int64(len(mdata)), Mode: 0644})
			tw.Write(mdata)
			for _, e := range entries {
				d := dataMap[e.Key]
				tw.WriteHeader(&tar.Header{Name: "data/" + e.Key, Size: int64(len(d)), Mode: 0644})
				tw.Write(d)
			}
			tw.Close()
			return
		}

		// Individual GET (for index listing).
		if r.Method == "GET" {
			w.WriteHeader(404)
			return
		}

		w.WriteHeader(405)
	}))
}

func TestGetBatch_ReturnsRequestedEntry(t *testing.T) {
	store := make(map[string][]byte)
	meta := make(map[string]map[string]string)
	srv := fakeBatchServer(t, store, meta)
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)

	// Manually store a compressed entry.
	compressed, _ := compressData([]byte("hello world"))
	store["go-buildcache/v1aabbccdd11223344"] = compressed
	meta["go-buildcache/v1aabbccdd11223344"] = map[string]string{"outputid": testOutputID("hello world")}

	// getBatch should find it via the batch endpoint.
	outputID, body, size, _, miss, err := b.getBatch("aabbccdd11223344", "go-buildcache/v1aabbccdd11223344")
	require.NoError(t, err)
	require.False(t, miss)
	require.Equal(t, testOutputID("hello world"), outputID)
	data, _ := io.ReadAll(body)
	require.Equal(t, "hello world", string(data))
	require.Equal(t, int64(11), size)
}

// TestGetBatch_RejectsCorruptEntry verifies the batch serve path applies the
// same end-to-end integrity check as individual GETs: a batched entry whose
// body does not hash to its advertised outputID is refused (miss), never served.
func TestGetBatch_RejectsCorruptEntry(t *testing.T) {
	store := make(map[string][]byte)
	meta := make(map[string]map[string]string)
	srv := fakeBatchServer(t, store, meta)
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)
	defer b.Close()

	// Advertise the hash of one body but store a different one under it.
	compressed, _ := compressData([]byte("a totally different body"))
	store["go-buildcache/v1aabbccdd11223344"] = compressed
	meta["go-buildcache/v1aabbccdd11223344"] = map[string]string{"outputid": testOutputID("the correct body")}

	_, _, _, _, miss, err := b.getBatch("aabbccdd11223344", "go-buildcache/v1aabbccdd11223344")
	require.NoError(t, err)
	require.True(t, miss, "a batched entry failing the checksum must be a miss")
	require.Equal(t, uint32(1), b.MissChecksum.Load())
	require.Equal(t, uint32(0), b.Stats.Hits.Load())
}

// TestGetBatch_MissingOutputIDNotCorrupt verifies that a batched entry with no
// outputid metadata is counted as a no-outputid miss (a metadata gap), not as a
// checksum mismatch / corruption — mirroring getIndividual.
func TestGetBatch_MissingOutputIDNotCorrupt(t *testing.T) {
	store := make(map[string][]byte)
	meta := make(map[string]map[string]string)
	srv := fakeBatchServer(t, store, meta)
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)
	defer b.Close()

	compressed, _ := compressData([]byte("body with no advertised outputid"))
	store["go-buildcache/v1aabbccdd11223344"] = compressed
	meta["go-buildcache/v1aabbccdd11223344"] = map[string]string{} // no outputid

	_, _, _, _, miss, err := b.getBatch("aabbccdd11223344", "go-buildcache/v1aabbccdd11223344")
	require.NoError(t, err)
	require.True(t, miss)
	require.Equal(t, uint32(1), b.MissNoOutputID.Load())
	require.Equal(t, uint32(0), b.MissChecksum.Load(), "missing outputid must not count as a checksum mismatch")
	require.Equal(t, uint32(0), b.Stats.Corrupt.Load(), "missing outputid must not mark the entry corrupt")
}

func TestGetBatch_PrefetchCallsOnBatchEntries(t *testing.T) {
	store := make(map[string][]byte)
	meta := make(map[string]map[string]string)
	srv := fakeBatchServer(t, store, meta)
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)

	// Store the requested entry and an extra entry (will be prefetched).
	compressed1, _ := compressData([]byte("entry one"))
	compressed2, _ := compressData([]byte("entry two"))
	store["go-buildcache/v1aaaa000000000001"] = compressed1
	meta["go-buildcache/v1aaaa000000000001"] = map[string]string{"outputid": testOutputID("entry one")}
	store["go-buildcache/v1aaaa000000000002"] = compressed2
	meta["go-buildcache/v1aaaa000000000002"] = map[string]string{"outputid": testOutputID("entry two")}

	var callbackEntries []BatchEntry
	b.OnBatchEntries = func(entries []BatchEntry) {
		callbackEntries = append(callbackEntries, entries...)
	}

	// Request one entry — server should also return the other as prefetch.
	outputID, body, _, _, miss, err := b.getBatch("aaaa000000000001", "go-buildcache/v1aaaa000000000001")
	require.NoError(t, err)
	require.False(t, miss)
	require.Equal(t, testOutputID("entry one"), outputID)
	data, _ := io.ReadAll(body)
	require.Equal(t, "entry one", string(data))

	// The callback should have been invoked with both entries.
	require.Len(t, callbackEntries, 2)
	keys := map[string]bool{}
	for _, e := range callbackEntries {
		keys[e.Key] = true
	}
	require.True(t, keys["go-buildcache/v1aaaa000000000001"])
	require.True(t, keys["go-buildcache/v1aaaa000000000002"])
}

func TestGetBatch_FallbackToIndividual(t *testing.T) {
	// Server that doesn't support /_batch/get (returns 404).
	store := make(map[string][]byte)
	meta := make(map[string]map[string]string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			body, _ := io.ReadAll(r.Body)
			key := r.URL.Path[len("/testbucket/"):]
			store[key] = body
			m := make(map[string]string)
			for k, v := range r.Header {
				lk := strings.ToLower(k)
				switch {
				case strings.HasPrefix(lk, "x-cache-meta-"):
					m[strings.TrimPrefix(lk, "x-cache-meta-")] = v[0]
				case strings.HasPrefix(lk, "x-amz-meta-"):
					// Deprecated alias; native wins if both present.
					mk := strings.TrimPrefix(lk, "x-amz-meta-")
					if _, ok := m[mk]; !ok {
						m[mk] = v[0]
					}
				}
			}
			meta[key] = m
			w.WriteHeader(200)
			return
		}
		if r.Method == "GET" {
			key := r.URL.Path[len("/testbucket/"):]
			if key == "_batch/get" {
				w.WriteHeader(404) // batch not supported
				return
			}
			d, ok := store[key]
			if !ok {
				w.WriteHeader(404)
				return
			}
			if m, ok := meta[key]; ok {
				for k, v := range m {
					w.Header().Set("X-Cache-Meta-"+k, v)
				}
			}
			w.WriteHeader(200)
			w.Write(d)
			return
		}
		w.WriteHeader(405)
	}))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)

	// Store a compressed entry and add the key to the index so
	// getIndividual can find it.
	compressed, _ := compressData([]byte("fallback data"))
	store["go-buildcache/v1aabbccdd11223344"] = compressed
	meta["go-buildcache/v1aabbccdd11223344"] = map[string]string{"outputid": testOutputID("fallback data")}

	// Add to index so getIndividual works.
	b.keysMu.Lock()
	b.keys.Add("go-buildcache/v1aabbccdd11223344")
	b.keysMu.Unlock()

	// getBatch should fall back to getIndividual when batch returns 404.
	outputID, body, _, _, miss, err := b.getBatch("aabbccdd11223344", "go-buildcache/v1aabbccdd11223344")
	require.NoError(t, err)
	require.False(t, miss)
	require.Equal(t, testOutputID("fallback data"), outputID)
	data, _ := io.ReadAll(body)
	require.Equal(t, "fallback data", string(data))
}

func TestGetBatch_Miss(t *testing.T) {
	// Server with empty store.
	srv := fakeBatchServer(t, make(map[string][]byte), make(map[string]map[string]string))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)

	_, _, _, _, miss, _ := b.getBatch("deadbeef00000000", "go-buildcache/v1deadbeef00000000")
	require.True(t, miss)
}

// TestGet_UsessBatchForUnknownKeys verifies that Get() routes through getBatch
// when the key is not in the local index.
func TestGet_UsesBatchForUnknownKeys(t *testing.T) {
	store := make(map[string][]byte)
	meta := make(map[string]map[string]string)
	srv := fakeBatchServer(t, store, meta)
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)

	// The fake server 404s on /_index, so the remote index loads empty; force a
	// non-empty index (production: a remote with content advertises a non-empty
	// index) so the requested-but-unindexed key still takes the batch path
	// instead of the empty-index fast-miss.
	b.indexEmpty = false

	// Key is NOT in b.keys index (simulating a fresh cache).
	compressed, _ := compressData([]byte("batch hit"))
	store["go-buildcache/v1aabbccdd11223344"] = compressed
	meta["go-buildcache/v1aabbccdd11223344"] = map[string]string{"outputid": testOutputID("batch hit")}

	outputID, body, _, _, miss, err := b.Get("aabbccdd11223344")
	require.NoError(t, err)
	require.False(t, miss)
	require.Equal(t, testOutputID("batch hit"), outputID)
	data, _ := io.ReadAll(body)
	require.Equal(t, "batch hit", string(data))
}

// TestGet_CoalescesConcurrentRequestsIntoOneHTTPRequest is the headline test
// for client-side batching: many concurrent Get callers must funnel through
// a single /_batch/get HTTP request rather than producing one request each.
func TestGet_CoalescesConcurrentRequestsIntoOneHTTPRequest(t *testing.T) {
	const N = 200

	store := make(map[string][]byte)
	meta := make(map[string]map[string]string)

	var batchHTTPCalls int32
	var maxKeysInOneRequest int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/testbucket/_batch/get" {
			w.WriteHeader(404)
			return
		}
		var req batchGetRequest
		json.NewDecoder(r.Body).Decode(&req)
		atomic.AddInt32(&batchHTTPCalls, 1)
		if int32(len(req.Keys)) > atomic.LoadInt32(&maxKeysInOneRequest) {
			atomic.StoreInt32(&maxKeysInOneRequest, int32(len(req.Keys)))
		}

		var entries []batchGetManifestEntry
		dataMap := map[string][]byte{}
		for _, key := range req.Keys {
			d, ok := store[key]
			if !ok {
				continue
			}
			entries = append(entries, batchGetManifestEntry{
				Key:      key,
				Size:     int64(len(d)),
				Metadata: meta[key],
			})
			dataMap[key] = d
		}
		manifest := batchGetManifest{Entries: entries}
		w.Header().Set("Content-Type", "application/x-tar")
		w.WriteHeader(200)
		tw := tar.NewWriter(w)
		mdata, _ := json.Marshal(manifest)
		tw.WriteHeader(&tar.Header{Name: "manifest.json", Size: int64(len(mdata)), Mode: 0644})
		tw.Write(mdata)
		for _, e := range entries {
			d := dataMap[e.Key]
			tw.WriteHeader(&tar.Header{Name: "data/" + e.Key, Size: int64(len(d)), Mode: 0644})
			tw.Write(d)
		}
		tw.Close()
	}))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)

	// The server 404s on /_index, so the remote index loads empty; force a
	// non-empty index so unindexed keys take the batch path this test exercises
	// (the empty-index fast-miss is covered by TestWebBackend_EmptyIndexSkipsBatch).
	b.indexEmpty = false

	// Fire N parallel Get calls for unknown keys.
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("%016x", i)
			_, _, _, _, _, _ = b.Get(id)
		}(i)
	}
	wg.Wait()

	// With batchMaxKeys=128 and the coalescer, N=200 callers should
	// fit in 2 HTTP requests at most (128 + 72), and almost certainly 1
	// if all goroutines arrive within the coalesce window.
	calls := atomic.LoadInt32(&batchHTTPCalls)
	require.LessOrEqual(t, calls, int32(3),
		"expected ≤3 HTTP requests for %d parallel Gets, got %d (no client-side batching)", N, calls)
	require.Greater(t, atomic.LoadInt32(&maxKeysInOneRequest), int32(1),
		"expected at least one HTTP request to carry multiple keys; max was %d", atomic.LoadInt32(&maxKeysInOneRequest))
}

// emptyIndexServer mimics a reachable but empty shared cache: GET /_index
// serves either a 404 (no blob yet) or, when indexBlob is non-nil, a real GBCI
// blob. It counts /_batch/get and PUT requests so tests can assert that cold
// Gets do (or do not) issue a batch round-trip. PUTs are accepted (200).
func emptyIndexServer(t *testing.T, batchGets, puts *atomic.Int64, indexBlob []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/testbucket/_index" && r.Method == http.MethodGet:
			if indexBlob == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			etag := indexETag(indexBlob)
			if r.Header.Get("If-None-Match") == etag {
				w.Header().Set("ETag", etag)
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", etag)
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(http.StatusOK)
			w.Write(indexBlob)
		case r.URL.Path == "/testbucket/_batch/get" && r.Method == http.MethodGet:
			batchGets.Add(1)
			// Empty remote: return an empty manifest (0 entries).
			manifest := batchGetManifest{}
			w.Header().Set("Content-Type", "application/x-tar")
			w.WriteHeader(200)
			tw := tar.NewWriter(w)
			mdata, _ := json.Marshal(manifest)
			tw.WriteHeader(&tar.Header{Name: "manifest.json", Size: int64(len(mdata)), Mode: 0644})
			tw.Write(mdata)
			tw.Close()
		case r.Method == http.MethodPut:
			puts.Add(1)
			io.Copy(io.Discard, r.Body)
			w.WriteHeader(200)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// TestWebBackend_EmptyIndexSkipsBatch is the regression for the cold-build
// waste: when the remote's authoritative key index is empty, a /_batch/get can
// only ever return zero entries, so the backend must skip the network entirely
// and miss cleanly. Before the fix, every cold key paid a round-trip per key
// (thousands of empty batches, ~27s) that the breaker never backed off (a
// 200-with-0-entries resets the failure streak).
func TestWebBackend_EmptyIndexSkipsBatch(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	var batchGets, puts atomic.Int64
	srv := emptyIndexServer(t, &batchGets, &puts, nil) // nil => 404 index => empty
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)
	defer b.Close()
	require.True(t, b.indexEmpty, "404 index must mark the remote index empty")

	// Several distinct cold keys must all miss with zero batch round-trips.
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("%016x", 0xc01d0000+i)
		_, _, _, _, miss, err := b.Get(id)
		require.NoError(t, err)
		require.True(t, miss, "a cold key against an empty remote must be a clean miss")
	}

	require.Equal(t, uint32(5), b.SkippedEmptyIndex.Load(),
		"every cold Get against an empty index must record an empty-index skip")
	require.Equal(t, int64(0), batchGets.Load(),
		"an empty remote index must issue ZERO /_batch/get round-trips")
}

// TestWebBackend_NonEmptyIndexStillBatches guards against over-broad skipping:
// when the index is non-empty (it holds at least one key), a cold key NOT in
// the index must still take the normal batch path — the remote may legitimately
// hold entries the partial index didn't list.
func TestWebBackend_NonEmptyIndexStillBatches(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	var batchGets, puts atomic.Int64

	indexed := set.New[string]()
	indexed.Add("go-buildcache/v1" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	blob := marshalIndex(indexed)

	srv := emptyIndexServer(t, &batchGets, &puts, blob)
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)
	defer b.Close()
	require.False(t, b.indexEmpty, "a one-key index must NOT be treated as empty")

	// A DIFFERENT cold key (not in the index) must still issue a batch GET.
	_, _, _, _, miss, err := b.Get("bbbbbbbbbbbbbbbb")
	require.NoError(t, err)
	require.True(t, miss, "the cold key is absent, so it misses")
	require.Equal(t, uint32(0), b.SkippedEmptyIndex.Load(),
		"a non-empty index must never skip the batch path")
	require.Equal(t, int64(1), batchGets.Load(),
		"a cold key against a non-empty index must still issue a /_batch/get")
}

// TestWebBackend_EmptyIndexStillPuts verifies the skip is GET-only: a Put on a
// cold/empty-index run must still upload, so the remote gets populated for the
// next build (which then sees a non-empty index and uses the normal path).
func TestWebBackend_EmptyIndexStillPuts(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	var batchGets, puts atomic.Int64
	srv := emptyIndexServer(t, &batchGets, &puts, nil)
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)
	defer b.Close()
	require.True(t, b.indexEmpty)

	body := []byte("a freshly compiled object")
	err = b.Put("deadbeefdeadbeef", testOutputID(string(body)), bytes.NewReader(body), int64(len(body)))
	require.NoError(t, err)

	require.Equal(t, int64(1), puts.Load(),
		"an empty-index run must still upload PUTs to populate the remote")
}
