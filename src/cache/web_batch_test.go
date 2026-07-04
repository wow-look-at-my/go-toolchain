package cache

import (
	"archive/tar"
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

	// The callback runs asynchronously (replies are distributed first) and
	// receives ONLY the non-requested prefetch entry: the requested one is
	// verified in the reply loop and written to the local tier by handleGet,
	// so feeding it to the callback too would verify and store it twice.
	// Close drains the coalescer (and the ingestion goroutine), making the
	// read below race-free.
	require.NoError(t, b.Close())
	require.Len(t, callbackEntries, 1)
	require.Equal(t, "go-buildcache/v1aaaa000000000002", callbackEntries[0].Key)
	require.True(t, callbackEntries[0].Prefetch)
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

	// The fake server 404s on /_index, so the index fetch fails and the key
	// set is NOT authoritative — exactly the recovery case in which unknown
	// keys must be batch-probed (the server may hold entries we can't see).
	require.False(t, b.indexAuthoritative)

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

	// The server 404s on /_index, so the index fetch fails and the key set is
	// not authoritative — unknown keys take the batch-probe path this test
	// exercises (with an authoritative index they would fast-miss instead).
	require.False(t, b.indexAuthoritative)

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

// TestWebBackend_EmptyBatchBackoffStopsProbing covers the probing recovery
// path under a useless remote: the index fetch FAILED (so cold keys are
// batch-probed — with an authoritative index they would fast-miss instead),
// and the server has none of this build's keys, so every /_batch/get returns
// zero entries. The consecutive-empty-batch backoff must trip after the
// threshold and stop probing, bounding the wasted round-trips instead of
// paying one batch per cold key.
func TestWebBackend_EmptyBatchBackoffStopsProbing(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	// Low threshold so the test trips quickly and deterministically.
	t.Setenv("GO_TOOLCHAIN_CACHE_EMPTY_BATCH_BACKOFF", "4")
	var batchGets, puts atomic.Int64

	srv := emptyIndexServer(t, &batchGets, &puts, nil) // 404 index => fetch failure
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)
	defer b.Close()
	require.False(t, b.indexAuthoritative, "fetch failure => non-authoritative => probing enabled")
	require.Equal(t, 4, b.emptyBatchBackoffThreshold)

	// Issue many distinct cold keys. The coalescer ships one key per batch here
	// (each Get blocks on its own reply before the next is issued), so each Get
	// that reaches the network is exactly one empty /_batch/get. After the
	// threshold the backoff trips and the remaining Gets skip the network.
	const nKeys = 40
	for i := 0; i < nKeys; i++ {
		id := fmt.Sprintf("%016x", 0xb0110000+i)
		_, _, _, _, miss, err := b.Get(id)
		require.NoError(t, err)
		require.True(t, miss, "a cold key the remote does not have must be a clean miss")
	}

	require.True(t, b.batchProbingDisabled.Load(),
		"consecutive empty batches must trip the backoff")
	require.Greater(t, b.SkippedBatchBackoff.Load(), uint32(0),
		"once tripped, cold Gets must record a batch-backoff skip")
	// Batch round-trips must be bounded by the threshold, NOT one-per-key. Allow a
	// little slack for the trip boundary, but it must be far below nKeys.
	require.LessOrEqual(t, batchGets.Load(), int64(b.emptyBatchBackoffThreshold+2),
		"batch GETs must be bounded by the backoff threshold, not one per cold key")
	require.Less(t, batchGets.Load(), int64(nKeys),
		"the backoff must prevent a round-trip per cold key")
	// Skips + actual batch round-trips must account for every cold key.
	require.Equal(t, uint32(nKeys), b.MissNotInIndex.Load(),
		"every cold key counts as not-in-index regardless of skip vs round-trip")
}

// TestWebBackend_BackoffResetsOnNonEmptyBatch verifies the backoff does NOT trip
// when the remote IS serving: a non-empty batch resets the consecutive-empty
// streak, so an interleaving of hits keeps batch probing enabled.
func TestWebBackend_BackoffResetsOnNonEmptyBatch(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("GO_TOOLCHAIN_CACHE_EMPTY_BATCH_BACKOFF", "4")

	store := make(map[string][]byte)
	meta := make(map[string]map[string]string)

	// Seed one served key so a batch requesting it comes back non-empty.
	hotID := "00000000000000ff"
	hotKey := "go-buildcache/v1" + hotID
	hotBody := []byte("served object that resets the streak")
	hotComp, err := compressData(hotBody)
	require.NoError(t, err)
	store[hotKey] = hotComp
	meta[hotKey] = map[string]string{"outputid": testOutputID(string(hotBody))}

	srv := fakeBatchServer(t, store, meta)
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)
	defer b.Close()
	// fakeBatchServer 404s on /_index, so the index is not authoritative and
	// cold keys take the batch-probe path this test exercises.
	require.False(t, b.indexAuthoritative)

	// Interleave: a few cold (empty-batch) keys, then a hot key that resets the
	// streak, repeated. The streak never reaches the threshold, so probing stays on.
	for round := 0; round < 6; round++ {
		for j := 0; j < 3; j++ {
			cold := fmt.Sprintf("%016x", 0xc01d0000+round*10+j)
			_, _, _, _, miss, err := b.Get(cold)
			require.NoError(t, err)
			require.True(t, miss)
		}
		// Hot key: served, non-empty batch → resets the empty streak.
		_, body, _, _, miss, err := b.Get(hotID)
		require.NoError(t, err)
		require.False(t, miss, "the seeded hot key must hit")
		if body != nil {
			io.Copy(io.Discard, body)
			body.Close()
		}
	}

	require.False(t, b.batchProbingDisabled.Load(),
		"a non-empty batch every few requests must keep the streak below threshold")
	require.Equal(t, uint32(0), b.SkippedBatchBackoff.Load(),
		"batch probing must stay enabled, so no backoff skips")
}
