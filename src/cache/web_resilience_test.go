package cache

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestWebBackend_CircuitBreakerTripsToLocalOnly is the regression for the
// backend-outage failure mode. Before this fix, every one of a build's
// thousands of cache operations independently hit a failing backend — there was
// no fallback, so an outage both stalled the build and amplified the very load
// (the 502 storm) that caused it. The breaker must trip after a bounded number
// of consecutive failures and then make ZERO further network requests for the
// rest of the run, degrading every GET to a clean miss and dropping every PUT.
func TestWebBackend_CircuitBreakerTripsToLocalOnly(t *testing.T) {
	t.Setenv("GO_TOOLCHAIN_CACHE_BREAKER_THRESHOLD", "3")
	t.Setenv("GO_TOOLCHAIN_CACHE_MAX_RETRIES", "0") // 1 request per op: deterministic counting

	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusBadGateway) // 502 for everything
	}))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "k", SecretKey: "s",
	})
	require.NoError(t, err)
	defer b.Close()

	// Capture the (single) fallback warning.
	var logBuf bytes.Buffer
	b.breakerLog = &logBuf

	// Construction's index fetch already 502'd once (streak=1). Prime a key so
	// every Get takes the individual path and counts as one request.
	const actionID = "deadbeef00000000"
	primeIndex(b, actionID)

	reqsAfterConstruction := requests.Load()
	require.Equal(t, int64(1), reqsAfterConstruction, "index fetch should be exactly one request with retries disabled")

	// Two more failures (gets 1 and 2) reach the threshold of 3 and trip the
	// breaker. Every subsequent get must skip the network entirely.
	const totalGets = 8
	for i := 0; i < totalGets; i++ {
		_, _, _, _, miss, err := b.Get(actionID)
		require.NoError(t, err)
		require.True(t, miss, "every get under a 502 outage must be a clean miss")
	}

	require.True(t, b.remoteDisabled(), "breaker must have tripped")

	// Only the two pre-trip gets hit the network; the rest were local-only.
	require.Equal(t, int64(3), requests.Load(),
		"after tripping, gets must make no further network requests (1 index + 2 pre-trip gets)")
	require.Equal(t, uint32(totalGets-2), b.MissCircuitOpen.Load(),
		"gets after the trip must be counted as circuit-open misses")

	// PUTs must also be dropped silently with no network request once tripped.
	require.NoError(t, b.Put(actionID, "ee00", strings.NewReader("body"), 4))
	require.Equal(t, int64(3), requests.Load(), "put after trip must not hit the network")
	require.Equal(t, uint32(0), b.Stats.Puts.Load())

	// Exactly one fallback warning, naming local-only mode.
	require.Equal(t, 1, strings.Count(logBuf.String(), "\n"), "expected exactly one fallback log line, got: %q", logBuf.String())
	require.Contains(t, logBuf.String(), "falling back to local-only")
}

// TestWebBackend_RetriesTransientThenRecovers proves bounded retries: a single
// GET retries a transient 502 up to maxRetries times, and if the backend
// recovers within that budget the GET succeeds rather than wastefully missing.
func TestWebBackend_RetriesTransientThenRecovers(t *testing.T) {
	t.Setenv("GO_TOOLCHAIN_CACHE_BREAKER_THRESHOLD", "0") // disable breaker for this test
	t.Setenv("GO_TOOLCHAIN_CACHE_MAX_RETRIES", "3")

	const actionID = "aabbccdd11223344"
	good := largePayload(2048)
	outputID := testOutputID(good)
	objectPath := "/testbucket/go-buildcache/v1" + actionID

	var objectReqs atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != objectPath {
			w.WriteHeader(http.StatusNotFound) // index etc.
			return
		}
		// Fail the first two object attempts, then serve the real body.
		if objectReqs.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("X-Cache-Meta-Outputid", outputID)
		w.WriteHeader(http.StatusOK)
		c, _ := compressData([]byte(good))
		w.Write(c)
	}))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "k", SecretKey: "s",
	})
	require.NoError(t, err)
	defer b.Close()
	primeIndex(b, actionID)

	gotOutputID, body, size, _, miss, err := b.Get(actionID)
	require.NoError(t, err)
	require.False(t, miss, "a backend that recovers within the retry budget must yield a hit, not a miss")
	require.Equal(t, outputID, gotOutputID)
	require.Equal(t, int64(len(good)), size)
	defer body.Close()
	require.Equal(t, int64(3), objectReqs.Load(), "should have retried twice before the 3rd attempt succeeded")
	require.Equal(t, uint32(1), b.Stats.Hits.Load())
}

// TestServer_RemoteOutageServesLocalCorrectly is the end-to-end regression for
// the incident: a total remote outage (every index/get/put returns 502) must
// never corrupt the build. The cache layer must degrade to clean misses, the
// local tier must keep serving exactly what was stored, and a GET for a
// never-stored key must miss — never a spurious hit or empty/garbage body.
func TestServer_RemoteOutageServesLocalCorrectly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway) // 502 for index, batch get, get, put
	}))
	defer srv.Close()

	web, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "k", SecretKey: "s",
	})
	require.NoError(t, err)

	lc, err := NewLocalCache(t.TempDir())
	require.NoError(t, err)
	s := NewServer(lc, web)

	stored := []byte("compiled package archive bytes for runtime")
	storedAction := []byte{0xaa, 0xbb, 0xcc, 0xdd, 0x00, 0x11, 0x22, 0x33, 0xaa, 0xbb, 0xcc, 0xdd, 0x00, 0x11, 0x22, 0x33}
	storedOutput := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	coldAction := []byte{0x99, 0x99, 0x99, 0x99, 0x99, 0x99, 0x99, 0x99, 0x99, 0x99, 0x99, 0x99, 0x99, 0x99, 0x99, 0x99}

	var input strings.Builder
	input.WriteString(makePutRequestRawBase64(Request{ID: 1, Command: CmdPut, ActionID: storedAction, OutputID: storedOutput, BodySize: int64(len(stored))}, string(stored)))
	input.WriteString(makeRequest(Request{ID: 2, Command: CmdGet, ActionID: storedAction})) // expect local hit
	input.WriteString(makeRequest(Request{ID: 3, Command: CmdGet, ActionID: coldAction}))   // expect clean miss
	input.WriteString(makeRequest(Request{ID: 4, Command: CmdClose}))

	var out bytes.Buffer
	require.NoError(t, s.Run(strings.NewReader(input.String()), &out))

	resps := map[int64]Response{}
	for _, r := range parseResponses(t, out.Bytes()) {
		resps[r.ID] = r
	}

	hit := resps[2]
	require.False(t, hit.Miss, "a locally-stored object must be served even when the remote is down")
	require.NotEmpty(t, hit.DiskPath)
	gotBytes, err := os.ReadFile(hit.DiskPath)
	require.NoError(t, err)
	require.Equal(t, stored, gotBytes, "the locally-served body must be exactly what was stored — never corrupt/empty")

	require.True(t, resps[3].Miss, "a never-stored key must miss cleanly under a remote outage, not spuriously hit")
}

// TestWebBackend_PutRetriesTransient503ThenSucceeds is the regression for the
// dropped-upload half of the CI-burst bug. The cache server's admission control
// sheds excess concurrent requests with 503 + Retry-After. Before this fix,
// WebBackend.Put issued a bare client.Do with NO retry, so a shed 503 silently
// dropped the object — it was never stored, and the shared /_index never filled
// under load. Put must now retry the 503 (honoring Retry-After) and store the
// object once the server admits it.
func TestWebBackend_PutRetriesTransient503ThenSucceeds(t *testing.T) {
	hermeticOTel(t)
	t.Setenv("GO_TOOLCHAIN_CACHE_MAX_RETRIES", "3")

	const actionID = "aabbccdd11223344"
	objectPath := "/testbucket/go-buildcache/v1" + actionID

	var putAttempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || r.URL.Path != objectPath {
			w.WriteHeader(http.StatusNotFound) // index etc.
			return
		}
		// Shed the first two PUT attempts the way admission control does:
		// 503 + Retry-After (0 keeps the test fast — the jittered base backoff
		// still applies, so this exercises the retry path without a real wait).
		if putAttempts.Add(1) < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "k", SecretKey: "s",
	})
	require.NoError(t, err)
	defer b.Close()
	// Drive the synchronous single-PUT retry path (the batch-unsupported
	// fallback) so the 503-retry-then-store outcome is observable from the Put
	// call directly. (The whole-batch 503 retry is covered in batchput_test.go.)
	b.batchPutUnsupported.Store(true)

	payload := largePayload(1024)
	outputID := testOutputID(payload)
	err = b.Put(actionID, outputID, strings.NewReader(payload), int64(len(payload)))
	require.NoError(t, err, "a 503-shed PUT must be retried and ultimately succeed, not silently dropped")
	require.Equal(t, int64(3), putAttempts.Load(), "should have retried twice before the 3rd PUT attempt was admitted")
	require.Equal(t, uint32(1), b.Stats.Puts.Load(), "the object must be recorded as stored")
	// A 503 shed is backpressure, not a backend fault — the breaker stays closed.
	require.False(t, b.remoteDisabled(), "503 admission sheds must not trip the breaker")
}

// TestWebBackend_Burst503DoesNotTripBreaker proves the breaker-classification
// half of the fix: a burst of 503 admission sheds (the CI-burst backpressure)
// must NOT trip the circuit breaker, so GETs keep attempting the remote and are
// never short-circuited to MissCircuitOpen. Before the fix, each 503 counted
// toward the shared breaker (breakerThreshold) and a burst disabled the remote
// cache for the rest of the run — "no hits ever".
func TestWebBackend_Burst503DoesNotTripBreaker(t *testing.T) {
	hermeticOTel(t)
	t.Setenv("GO_TOOLCHAIN_CACHE_BREAKER_THRESHOLD", "3")
	t.Setenv("GO_TOOLCHAIN_CACHE_MAX_RETRIES", "0") // 1 request per op

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusServiceUnavailable) // 503 for everything
	}))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "k", SecretKey: "s",
	})
	require.NoError(t, err)
	defer b.Close()

	const actionID = "deadbeef00000000"
	primeIndex(b, actionID)

	// A burst of GETs that all get 503 — well past breakerThreshold.
	for i := 0; i < 12; i++ {
		_, _, _, _, miss, err := b.Get(actionID)
		require.NoError(t, err)
		require.True(t, miss, "a 503 still degrades a single GET to a clean miss")
	}

	require.False(t, b.remoteDisabled(), "a burst of 503 sheds must NOT trip the breaker (it is backpressure, not a fault)")
	require.Equal(t, uint32(0), b.MissCircuitOpen.Load(), "no GET should be short-circuited as a circuit-open miss")

	// PUTs that all get 503 likewise must not trip the breaker. Drive the
	// synchronous single-PUT path so each Put's 503 outcome is applied before the
	// next iteration (the batch path would coalesce these into one request).
	b.batchPutUnsupported.Store(true)
	for i := 0; i < 12; i++ {
		// PUT errors (it ran out of retry budget) but the breaker stays closed.
		_ = b.Put(actionID, testOutputID(largePayload(64)), strings.NewReader(largePayload(64)), 64)
		// Re-claim slot: removeClaimed already ran on the failed Put, so the next
		// Put for the same key is attempted again.
	}
	require.False(t, b.remoteDisabled(), "a burst of 503 PUT sheds must NOT trip the breaker either")
}

// TestWebBackend_Burst502TripsBreaker is the contrast control: a burst of 502
// (a genuine backend fault, not backpressure) DOES still trip the breaker. This
// proves the 503 carve-out in breakerFault is surgical — only 503 is exempt,
// every other transient status still protects the build from hammering a truly
// unhealthy backend.
func TestWebBackend_Burst502TripsBreaker(t *testing.T) {
	hermeticOTel(t)
	t.Setenv("GO_TOOLCHAIN_CACHE_BREAKER_THRESHOLD", "3")
	t.Setenv("GO_TOOLCHAIN_CACHE_MAX_RETRIES", "0")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway) // 502 for everything
	}))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "k", SecretKey: "s",
	})
	require.NoError(t, err)
	defer b.Close()
	var logBuf bytes.Buffer
	b.breakerLog = &logBuf

	const actionID = "deadbeef00000000"
	primeIndex(b, actionID)

	// Construction's index fetch 502'd once (streak=1); two more GET failures
	// reach the threshold of 3 and trip the breaker.
	for i := 0; i < 8; i++ {
		_, _, _, _, miss, err := b.Get(actionID)
		require.NoError(t, err)
		require.True(t, miss)
	}
	require.True(t, b.remoteDisabled(), "a burst of 502 (a real fault) must still trip the breaker")
	require.Contains(t, logBuf.String(), "falling back to local-only")
}

// TestParseRetryAfter covers the Retry-After header parsing helper.
func TestParseRetryAfter(t *testing.T) {
	mk := func(v string) *http.Response {
		r := &http.Response{Header: http.Header{}}
		if v != "" {
			r.Header.Set("Retry-After", v)
		}
		return r
	}
	require.Equal(t, time.Duration(0), parseRetryAfter(nil), "nil response")
	require.Equal(t, time.Duration(0), parseRetryAfter(mk("")), "absent header")
	require.Equal(t, time.Duration(0), parseRetryAfter(mk("garbage")), "unparseable")
	require.Equal(t, time.Duration(0), parseRetryAfter(mk("0")), "zero seconds")
	require.Equal(t, 1*time.Second, parseRetryAfter(mk("1")), "delta-seconds")
	// Capped at retryMaxDelay.
	require.Equal(t, retryMaxDelay, parseRetryAfter(mk("3600")), "large delta capped at retryMaxDelay")
}

// sanity: the batch request shape is unchanged by the resilience wrapper.
func TestBatchGetRequest_JSONShape(t *testing.T) {
	data, err := json.Marshal(batchGetRequest{Keys: []string{"a", "b"}, Prefetch: true})
	require.NoError(t, err)
	require.JSONEq(t, `{"keys":["a","b"],"prefetch":true}`, string(data))
}
