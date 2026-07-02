package cache

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wow-look-at-my/go-containers/set"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// errLogged signals that an error has already been reported to stderr at
// the web layer. Outer callers should suppress further logging for it.
var errLogged = errors.New("web: already logged")

// MaxConnsPerHost is the HTTP connection pool size for the remote cache.
const MaxConnsPerHost = 64

// WebConfig holds the configuration for a web cache backend.
type WebConfig struct {
	Bucket    string // Required. Empty bucket disables the backend.
	Endpoint  string // Endpoint URL (e.g. "https://cache.example.com"). Required.
	Prefix    string // Key prefix (defaults to "go-buildcache/").
	AccessKey string // Basic Auth username
	SecretKey string // Basic Auth password
	Version   string // go-toolchain version, stored as object metadata
}

// WebBackend stores cache objects in a remote web server with LZ4 compression.
// GETs use the server's batch endpoint to fetch entries with prefetch support,
// proactively populating the local cache with related entries. PUTs upload
// entries individually.
type WebBackend struct {
	client     *http.Client
	bucket     string
	prefix     string
	endpoint   string
	accessKey  string
	secretKey  string
	version    string // go-toolchain version for object metadata
	Stats      CacheStats
	Pool       ConcurrencyTracker // HTTP connection pool usage (shared across all Servers)
	Latency    *LatencyStats      // optional; set by Server for sub-operation tracking
	keysMu     sync.RWMutex
	keys       set.Set[string] // known keys, from the startup index fetch + Put claims
	indexEmpty bool            // remote index was empty at startup: nothing to batch-probe for
	// indexAuthoritative is true when the startup key set came from a fresh,
	// server-confirmed index (200 parsed, or 304 validating our disk copy).
	// An authoritative index makes "key absent" trustworthy: a probe for an
	// absent key can only 404/return-empty by construction, so cold keys miss
	// cleanly without a round-trip. When the fetch FAILED (stale disk copy or
	// empty set), absences prove nothing and cold keys are batch-probed — the
	// recovery path for a client that doesn't know what the server holds.
	indexAuthoritative bool
	missesMu           sync.RWMutex
	knownMiss          set.Set[string] // keys confirmed absent from remote this session

	// Consecutive-empty-batch backoff. Separate from the circuit breaker: an
	// empty-but-200 /_batch/get is a HEALTHY backend that simply has none of this
	// build's keys (a large remote index that does not overlap this build, or a
	// stale/useless one). The breaker never trips for it (a 200 resets the
	// failure streak), so without this, a populated-but-non-overlapping index
	// still pays a batch round-trip per cold key. After
	// emptyBatchBackoffThreshold consecutive zero-entry batches we conclude the
	// remote holds nothing useful for this build and stop probing for the rest of
	// the run; any non-empty batch resets the streak (the remote IS serving).
	emptyBatchBackoffThreshold int          // 0 disables the backoff
	consecutiveEmptyBatches    atomic.Int64 // current run of zero-entry batches
	batchProbingDisabled       atomic.Bool  // true once backoff has tripped
	batchBackoffLogOnce        sync.Once    // logs the disable notice exactly once

	// OnBatchEntries is called when a batch GET returns prefetch entries.
	// The caller (Server/Daemon) uses this to populate the local cache,
	// turning future remote GETs into local hits.
	OnBatchEntries func(entries []BatchEntry)

	// Miss reason counters for diagnostics.
	MissNotInIndex  AtomicCounter
	MissHTTP404     AtomicCounter
	MissHTTPError   AtomicCounter
	MissNoOutputID  AtomicCounter
	MissReadBody    AtomicCounter
	MissDecompress  AtomicCounter
	MissChecksum    AtomicCounter
	MissBuildID     AtomicCounter
	MissModuleIndex AtomicCounter // module-index blobs refused: unverifiable under a key
	MissNetwork     AtomicCounter
	MissCircuitOpen AtomicCounter // gets served as misses because the breaker tripped

	// SkippedEmptyIndex counts cold-key Gets that returned a clean miss without
	// a /_batch/get round-trip because the remote's authoritative key index was
	// empty at startup: an empty remote provably holds nothing to fetch.
	SkippedEmptyIndex AtomicCounter

	// SkippedBatchBackoff counts cold-key Gets that returned a clean miss without
	// a /_batch/get round-trip because the consecutive-empty-batch backoff had
	// tripped: the remote repeatedly returned zero-entry batches this run, so it
	// provably holds nothing useful for this build. Only reachable when the
	// index fetch failed (with an authoritative index, absent keys are never
	// probed in the first place — see SkippedNotInIndex).
	SkippedBatchBackoff AtomicCounter

	// SkippedNotInIndex counts cold-key Gets that returned a clean miss without
	// a round-trip because the run's AUTHORITATIVE (fresh, non-empty) index
	// does not list the key: probing it would 404/return-empty by construction.
	// Before this policy, exactly these keys were batch-probed, every batch
	// came back empty, and ~24 wasted round-trips later the empty-batch backoff
	// disabled batching for the whole run — killing prefetch too.
	SkippedNotInIndex AtomicCounter

	// Reclaimed404 counts index-claimed keys dropped after the server
	// authoritatively reported them absent (a 404, or missing from a 200 batch
	// response): the stale claim is removed from the known-keys set so the PUT
	// path re-uploads the object instead of skipping it as already-present —
	// previously such a key was a permanent forced miss.
	Reclaimed404 AtomicCounter

	// PUT-side skip/refusal counters. WebBackend.Put returns nil for several
	// distinct non-upload outcomes; without these the gap between local puts
	// and actual uploads (581 uploads for 7589 local puts in the baseline) is
	// invisible in the web summary.
	PutSkippedKnown    AtomicCounter // key already in the index or claimed by an in-flight upload
	PutRefusedBuildID  AtomicCounter // refused: build-id action mismatch (mis-keyed object)
	PutRefusedModIndex AtomicCounter // refused: Go module index (never published to the shared cache)

	// Failure-handling resilience. A backend outage (5xx, timeout, reset) must
	// never stall or corrupt a build: every remote op degrades to a clean miss
	// (GET) or a silent drop (PUT). To stop a build's thousands of cache ops
	// from each independently hammering a failing backend — amplifying the very
	// load that caused the outage — a circuit breaker trips to local-only mode
	// after breakerThreshold consecutive failures, logged exactly once.
	maxRetries       int          // bounded retries for transient GET failures
	breakerThreshold int          // consecutive failures before tripping (0 disables)
	breakerFailures  atomic.Int64 // current consecutive-failure streak
	breakerTripped   atomic.Bool  // true once the breaker has tripped
	breakerLogOnce   sync.Once    // ensures the fallback warning is logged once
	breakerLog       io.Writer    // where the fallback warning goes (nil => os.Stderr)

	tracer *cacheTracer // nil when OTel is not configured
	errLog *httpErrLogger

	// Client-side batch coalescer: many concurrent Get callers funnel
	// their keys through batchReqCh, the worker collects them on a short
	// time window and ships them as one /_batch/get HTTP request, then
	// fans the response back out via per-request reply channels.
	batchReqCh  chan batchReq
	batchStop   chan struct{}
	batchDone   chan struct{}
	batchHTTPWG sync.WaitGroup
}

type batchReq struct {
	actionID string
	key      string
	resp     chan batchResp
}

type batchResp struct {
	outputID string
	body     io.ReadCloser
	size     int64
	t        time.Time
	miss     bool
}

const (
	batchMaxKeys      = 128
	batchCoalesceWait = 10 * time.Millisecond
	batchReqChBuf     = 1024
)

// NewWebBackend creates a web backend from the given config.
// Returns nil if bucket is empty.
func NewWebBackend(cfg WebConfig) (*WebBackend, error) {
	if cfg.Bucket == "" {
		return nil, nil
	}
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("web: endpoint is required")
	}
	prefix := cfg.Prefix
	if prefix == "" {
		prefix = "go-buildcache/"
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	accessKey := cfg.AccessKey
	secretKey := cfg.SecretKey
	if accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("web: access key and secret key are required")
	}

	endpoint := strings.TrimRight(cfg.Endpoint, "/")
	if !strings.HasPrefix(endpoint, "https://") && !strings.HasPrefix(endpoint, "http://") {
		endpoint = "https://" + endpoint
	}

	// Tune the transport for high-throughput cache uploads. The default Go
	// transport only keeps 2 idle connections per host, which forces a new
	// TCP+TLS handshake for nearly every request. We allow up to 64
	// concurrent connections and keep them all alive in the idle pool.
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSClientConfig:       &tls.Config{},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          MaxConnsPerHost,
		MaxIdleConnsPerHost:   MaxConnsPerHost,
		MaxConnsPerHost:       MaxConnsPerHost,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}

	b := &WebBackend{
		maxRetries:                 envInt("GO_TOOLCHAIN_CACHE_MAX_RETRIES", defaultMaxRetries),
		breakerThreshold:           envInt("GO_TOOLCHAIN_CACHE_BREAKER_THRESHOLD", defaultBreakerThreshold),
		emptyBatchBackoffThreshold: envInt("GO_TOOLCHAIN_CACHE_EMPTY_BATCH_BACKOFF", defaultEmptyBatchBackoff),
		client: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return fmt.Errorf("stopped after 10 redirects")
				}
				// Preserve original method — Go changes PUT/POST to GET on 301/302.
				orig := via[0]
				req.Method = orig.Method
				req.Body = orig.Body
				req.GetBody = orig.GetBody
				req.ContentLength = orig.ContentLength
				for key, vals := range orig.Header {
					req.Header[key] = vals
				}
				return nil
			},
		},
		bucket:    cfg.Bucket,
		prefix:    prefix,
		endpoint:  endpoint,
		accessKey: accessKey,
		secretKey: secretKey,
		version:   cfg.Version,
	}
	b.tracer = newCacheTracer(os.Stderr)
	b.errLog = newHTTPErrLogger(os.Stderr, httpErrFlushInterval, b.tracer)
	b.batchReqCh = make(chan batchReq, batchReqChBuf)
	b.batchStop = make(chan struct{})
	b.batchDone = make(chan struct{})
	go b.batchCoalescer()
	b.keys, b.indexAuthoritative = b.loadOrFetchIndex()
	b.indexEmpty = b.keys.Len() == 0
	b.knownMiss = set.New[string]()
	if b.indexAuthoritative {
		fmt.Fprintf(os.Stderr, "cacheprog: web index: %d keys\n", b.keys.Len())
	} else {
		fmt.Fprintf(os.Stderr, "cacheprog: web index: fetch failed; using %d cached keys (batch probing enabled)\n", b.keys.Len())
	}
	return b, nil
}

func (b *WebBackend) key(actionID string) string {
	return b.prefix + "v1" + actionID
}

func (b *WebBackend) url(key string) string {
	return b.endpoint + "/" + b.bucket + "/" + key
}

// Get retrieves a cached object.
//
// Routing policy (the batch endpoint is the primary fetch path):
//
//   - Key in the index (an expected hit): fetch through the coalescing batch
//     endpoint — one round-trip serves up to batchMaxKeys concurrent callers
//     and carries the server's prefetch entries from the same build. This is
//     what makes prefetch function at all: batches of only-absent keys (the
//     old policy) return zero entries by construction, which both wasted the
//     round-trips and tripped the empty-batch backoff on every cold run,
//     disabling batching — and prefetch — for the rest of the build. Servers
//     without /_batch/get still work: sendBatch falls back to individual GETs.
//
//   - Key absent from an AUTHORITATIVE index (fresh 200/304 this run): miss
//     cleanly with no network — the server itself said it doesn't have it.
//
//   - Key absent but the index fetch FAILED: we don't know what the server
//     holds, so batch-probe the key (the recovery path), bounded by the
//     consecutive-empty-batch backoff.
func (b *WebBackend) Get(actionID string) (outputID string, body io.ReadCloser, size int64, t time.Time, miss bool, err error) {
	// Circuit breaker tripped: the remote is known-unhealthy this run, so skip
	// the network entirely and report a clean miss. The build recomputes from
	// source — slower, always correct — instead of waiting on a failing backend.
	if b.remoteDisabled() {
		b.MissCircuitOpen.Increment()
		return "", nil, 0, time.Time{}, true, nil
	}
	key := b.key(actionID)
	if b.keyKnown(key) {
		return b.getBatch(actionID, key)
	}

	// Key not in index — check if we already know it's absent.
	b.missesMu.RLock()
	alreadyMissed := b.knownMiss.Contains(key)
	b.missesMu.RUnlock()
	if alreadyMissed {
		b.MissNotInIndex.Increment()
		return "", nil, 0, time.Time{}, true, nil
	}

	b.MissNotInIndex.Increment()
	if b.indexAuthoritative {
		// The server's own key index for this run says the key is absent, so a
		// probe can only come back empty by construction. Miss cleanly instead
		// of paying a round-trip per cold key (on a cold CI build, thousands).
		if b.indexEmpty {
			b.SkippedEmptyIndex.Increment()
		} else {
			b.SkippedNotInIndex.Increment()
		}
		return "", nil, 0, time.Time{}, true, nil
	}
	if b.batchProbingOff() {
		// No authoritative index this run AND the remote has returned enough
		// consecutive zero-entry batches that the backoff tripped: it provably
		// holds nothing useful for this build. Miss without the round-trip.
		b.SkippedBatchBackoff.Increment()
		return "", nil, 0, time.Time{}, true, nil
	}
	return b.getBatch(actionID, key)
}

// keyKnown reports whether key is in the known-keys set (the startup index
// plus optimistic Put claims).
func (b *WebBackend) keyKnown(key string) bool {
	b.keysMu.RLock()
	defer b.keysMu.RUnlock()
	return b.keys.Contains(key)
}

// reclaimAbsent records an AUTHORITATIVE "not present" answer from the server
// for key (an individual 404, or absence from a 200 batch response). If our
// index claimed the key, that claim is stale (evicted server-side, or a stale
// index entry): drop it so Put's check-and-claim re-uploads the object —
// previously the claim made every future Put skip as already-present,
// leaving the key a permanent forced miss. The key is also marked knownMiss
// so Gets stop re-asking this run. Returns whether a stale claim was dropped.
func (b *WebBackend) reclaimAbsent(key string) bool {
	b.keysMu.Lock()
	removed := b.keys.Contains(key)
	if removed {
		b.keys.Remove(key)
	}
	b.keysMu.Unlock()
	if removed {
		b.Reclaimed404.Increment()
	}
	b.missesMu.Lock()
	b.knownMiss.Add(key)
	b.missesMu.Unlock()
	return removed
}

// getIndividual fetches a single object stored under an individual cache key.
// parentCtx, when non-nil and carrying a valid span, becomes the parent
// of the emitted cacheprog.web.get span — used by sendBatch's 404/405
// fallback path so individual GETs nest under the batch span instead of
// detaching to the run-level root.
func (b *WebBackend) getIndividual(parentCtx context.Context, actionID, key string) (string, io.ReadCloser, int64, time.Time, bool, error) {
	_, span := b.tracer.StartFromCtx(parentCtx, "cacheprog.web.get",
		attribute.String("cacheprog.action_id", shortID(actionID)),
	)
	defer span.End()

	req, err := http.NewRequest("GET", b.url(key), nil)
	if err != nil {
		span.SetStatus(codes.Error, "build request")
		return "", nil, 0, time.Time{}, true, nil
	}
	b.signRequest(req)

	b.Pool.Acquire()
	httpStart := time.Now()
	resp, err := b.doRetryGET(req)
	if err != nil {
		b.Pool.Release()
		b.MissNetwork.Increment()
		b.noteRemoteResult(true)
		markSpanErr(span, "network", err)
		markSpanMiss(span, "network")
		fmt.Fprintf(os.Stderr, "cacheprog: web get %s: %v\n", shortID(actionID), err)
		return "", nil, 0, time.Time{}, true, nil
	}
	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))

	if resp.StatusCode == 404 {
		resp.Body.Close()
		b.Pool.Release()
		b.MissHTTP404.Increment()
		b.noteRemoteResult(false) // a definitive miss from a healthy backend
		// Drop any stale index claim so the PUT path re-uploads the object;
		// without this the key 404s forever and is never re-published.
		b.reclaimAbsent(key)
		markSpanMiss(span, "http_404")
		return "", nil, 0, time.Time{}, true, nil
	}
	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		b.Pool.Release()
		b.MissHTTPError.Increment()
		b.noteRemoteResult(transientStatus(resp.StatusCode))
		markSpanMiss(span, fmt.Sprintf("http_%d", resp.StatusCode))
		b.errLog.Record("web get", resp.StatusCode, actionID, string(respBody))
		return "", nil, 0, time.Time{}, true, nil
	}
	b.noteRemoteResult(false) // 200: backend healthy, reset the failure streak

	// Native metadata header, with a fallback to the deprecated S3-style header
	// so a new client still reads the outputid from an older (not-yet-upgraded)
	// cache server that only emits X-Amz-Meta-*.
	outputID := resp.Header.Get("X-Cache-Meta-Outputid")
	if outputID == "" {
		outputID = resp.Header.Get("X-Amz-Meta-Outputid")
	}
	if outputID == "" {
		resp.Body.Close()
		b.Pool.Release()
		b.MissNoOutputID.Increment()
		markSpanMiss(span, "no_outputid")
		fmt.Fprintf(os.Stderr, "cacheprog: web get %s: missing outputid metadata\n", shortID(actionID))
		return "", nil, 0, time.Time{}, true, nil
	}

	compressed, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	b.Pool.Release()
	if b.Latency != nil {
		b.Latency.HTTPGet.Record(time.Since(httpStart))
	}
	if err != nil {
		b.MissReadBody.Increment()
		markSpanErr(span, "read_body", err)
		markSpanMiss(span, "read_body")
		fmt.Fprintf(os.Stderr, "cacheprog: web get %s: read body: %v\n", shortID(actionID), err)
		return "", nil, 0, time.Time{}, true, nil
	}

	decompressStart := time.Now()
	decompressed, err := decompressData(compressed)
	if b.Latency != nil {
		b.Latency.Decompress.Record(time.Since(decompressStart))
	}
	if err != nil {
		b.MissDecompress.Increment()
		markSpanErr(span, "decompress", err)
		markSpanMiss(span, "decompress")
		fmt.Fprintf(os.Stderr, "cacheprog: web get %s: decompress: %v\n", shortID(actionID), err)
		return "", nil, 0, time.Time{}, true, nil
	}

	// End-to-end integrity check: the body must hash to its advertised
	// outputID (the go content hash). A mismatch means the remote object is
	// corrupt — truncated, badly decoded, or poisoned/rotted in the remote cache — and
	// serving it would feed the go command a damaged object (e.g. a module
	// index -> "corrupt index"). Refuse to serve, and drop the key from the
	// index so the next recompute re-uploads (overwrites) it clean instead of
	// skipping the Put as already-present.
	if got, ok := outputIDMatches(outputID, decompressed); !ok {
		b.MissChecksum.Increment()
		b.Stats.Corrupt.Increment()
		markSpanMiss(span, "checksum")
		b.removeClaimed(key)
		fmt.Fprintf(os.Stderr, "cacheprog: web get %s: body checksum mismatch (want outputid=%s, got sha256=%s, len=%d); evicting and treating as miss\n",
			shortID(actionID), shortID(outputID), shortID(got), len(decompressed))
		return "", nil, 0, time.Time{}, true, nil
	}

	// Cross-contamination guard: a compiled package self-certifies its action
	// key in its build id. A body that hashes to its outputID but whose build id
	// belongs to a DIFFERENT action is a poisoned mapping (the wrong object under
	// this key) that the hash check above cannot catch -- e.g. internal/reflectlite
	// export data served for the `runtime` action, surfacing as "imported as
	// reflectlite". Refuse it, and evict the key so a recompute re-uploads
	// (overwrites) the correct object.
	if act, ok := buildIDMatchesAction(actionID, decompressed); !ok {
		b.MissBuildID.Increment()
		b.Stats.Corrupt.Increment()
		markSpanMiss(span, "buildid_mismatch")
		b.removeClaimed(key)
		fmt.Fprintf(os.Stderr, "cacheprog: web get %s: build-id action mismatch (want action=%s, got action=%s, len=%d); evicting and treating as miss\n",
			shortID(actionID), expectedBuildIDAction(actionID), act, len(decompressed))
		return "", nil, 0, time.Time{}, true, nil
	}

	// Module-index guard: a Go module index blob carries no build id and does not
	// self-certify which directory it indexes, so neither the outputID hash nor
	// the build-id check can prove it belongs under this key (see isGoModuleIndex).
	// A wrong one is silently fatal at package load ("package runtime is not in
	// std" / "corrupt index"), so refuse it from the shared cache and let cmd/go
	// recompute it locally (a cheap directory read). Evict the claim so the
	// recompute is free to re-Put.
	if isGoModuleIndex(decompressed) {
		b.MissModuleIndex.Increment()
		markSpanMiss(span, "module_index")
		b.removeClaimed(key)
		fmt.Fprintf(os.Stderr, "cacheprog: web get %s: refusing module-index blob (unverifiable under this key, len=%d); treating as miss\n",
			shortID(actionID), len(decompressed))
		return "", nil, 0, time.Time{}, true, nil
	}

	t := time.Now()
	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		if parsed, parseErr := time.Parse(http.TimeFormat, lm); parseErr == nil {
			t = parsed
		}
	}

	b.Stats.Hits.Increment()
	span.SetAttributes(attribute.Bool("cacheprog.hit", true),
		attribute.Int("cacheprog.bytes_compressed", len(compressed)),
		attribute.Int("cacheprog.bytes_uncompressed", len(decompressed)),
		attribute.String("cacheprog.label", describeData(decompressed)))
	return outputID, io.NopCloser(bytes.NewReader(decompressed)), int64(len(decompressed)), t, false, nil
}

// getBatch enqueues this key on the coalescer and waits for the result.
// Multiple concurrent callers funnel into the same outgoing HTTP request
// instead of each making their own — see batchCoalescer / sendBatch.
func (b *WebBackend) getBatch(actionID, key string) (string, io.ReadCloser, int64, time.Time, bool, error) {
	respCh := make(chan batchResp, 1)
	select {
	case b.batchReqCh <- batchReq{actionID: actionID, key: key, resp: respCh}:
	case <-b.batchStop:
		// Backend is closing — return miss so the caller can fall back.
		return "", nil, 0, time.Time{}, true, nil
	}
	r := <-respCh
	return r.outputID, r.body, r.size, r.t, r.miss, nil
}

// Put stores a cached object with LZ4 compression.
// Skips upload if the key is already in the index.
func (b *WebBackend) Put(actionID, outputID string, body io.Reader, bodySize int64) error {
	// Circuit breaker tripped: drop the upload silently. PUTs are best-effort —
	// a dropped upload only costs a future cache miss, never build correctness —
	// and continuing to push at a failing backend would amplify its overload.
	if b.remoteDisabled() {
		return nil
	}
	key := b.key(actionID)

	// Atomically check-and-claim: if the key is already known (or being
	// uploaded by another goroutine), skip immediately.
	b.keysMu.Lock()
	if b.keys.Contains(key) {
		b.keysMu.Unlock()
		b.PutSkippedKnown.Increment()
		return nil
	}
	b.keys.Add(key)
	b.keysMu.Unlock()

	_, span := b.tracer.Start("cacheprog.web.put",
		attribute.String("cacheprog.action_id", shortID(actionID)),
		attribute.Int64("cacheprog.bytes_uncompressed", bodySize))
	defer span.End()

	var uploaded bool
	defer func() {
		if !uploaded {
			b.removeClaimed(key)
		}
	}()

	raw, err := io.ReadAll(body)
	if err != nil {
		markSpanErr(span, "read body", err)
		return fmt.Errorf("web put read: %w", err)
	}

	// Write-side cross-contamination guard: never publish a compiled package to
	// the shared cache under a key that disagrees with the package's own build
	// id. The body<->outputID hash is self-consistent for a mis-keyed object, so
	// only this check stops a swapped (actionID, object) pair from poisoning the
	// remote cache for every other consumer. The deferred removeClaimed releases
	// the optimistic index claim, so a later correct Put for this key still runs.
	if act, ok := buildIDMatchesAction(actionID, raw); !ok {
		b.PutRefusedBuildID.Increment()
		markSpanMiss(span, "buildid_mismatch")
		fmt.Fprintf(os.Stderr, "cacheprog: web put %s: refusing upload, build-id action mismatch (want action=%s, got action=%s); object does not belong under this key\n",
			shortID(actionID), expectedBuildIDAction(actionID), act)
		return nil
	}

	// Never publish a Go module index to the shared cache. It cannot be verified
	// against an action key on the way back in (see isGoModuleIndex), so a
	// consumer has no way to tell a correct one from a mis-keyed (build-breaking)
	// one -- the read side refuses all of them. Uploading is therefore pure
	// downside: wasted bytes plus a standing poison vector for any client. Every
	// consumer recomputes the index locally, so dropping the upload costs nothing.
	if isGoModuleIndex(raw) {
		b.PutRefusedModIndex.Increment()
		markSpanMiss(span, "module_index")
		return nil
	}

	compressStart := time.Now()
	compressed, err := compressData(raw)
	if b.Latency != nil {
		b.Latency.Compress.Record(time.Since(compressStart))
	}
	if err != nil {
		markSpanErr(span, "compress", err)
		return fmt.Errorf("web put compress: %w", err)
	}
	span.SetAttributes(attribute.Int("cacheprog.bytes_compressed", len(compressed)))

	req, err := http.NewRequest("PUT", b.url(key), bytes.NewReader(compressed))
	if err != nil {
		span.SetStatus(codes.Error, "build request")
		return fmt.Errorf("web put request: %w", err)
	}
	req.ContentLength = int64(len(compressed))
	req.Header.Set("X-Cache-Meta-Outputid", outputID)
	req.Header.Set("X-Cache-Meta-Object-Type", detectObjectType(raw))
	req.Header.Set("X-Cache-Meta-Body-Size", strconv.FormatInt(bodySize, 10))
	req.Header.Set("X-Cache-Meta-Compression", "lz4")
	req.Header.Set("X-Cache-Meta-Created", time.Now().UTC().Format(time.RFC3339))
	if b.version != "" {
		req.Header.Set("X-Cache-Meta-Toolchain-Version", b.version)
	}
	if goVer, target := parseArchiveHeader(raw); goVer != "" {
		req.Header.Set("X-Cache-Meta-Go-Version", goVer)
		req.Header.Set("X-Cache-Meta-Target", target)
	}
	if pkg := parseImportPath(raw); pkg != "" {
		req.Header.Set("X-Cache-Meta-Pkg", pkg)
	}
	if files := parseSourceFiles(raw); len(files) > 0 {
		req.Header.Set("X-Cache-Meta-Src", strings.Join(files, " "))
	}
	b.signRequest(req)

	b.Pool.Acquire()
	httpStart := time.Now()
	resp, err := b.client.Do(req)
	b.Pool.Release()
	if b.Latency != nil && err == nil {
		b.Latency.HTTPPut.Record(time.Since(httpStart))
	}
	if err != nil {
		b.noteRemoteResult(true)
		markSpanErr(span, "network", err)
		fmt.Fprintf(os.Stderr, "cacheprog: web put %s: %v\n", shortID(actionID), err)
		return err
	}
	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
	// Drain and close body so the connection is returned to the pool.
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		b.noteRemoteResult(transientStatus(resp.StatusCode))
		span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d", resp.StatusCode))
		b.errLog.Record("web put", resp.StatusCode, actionID, string(respBody))
		return fmt.Errorf("web put: HTTP %d: %w", resp.StatusCode, errLogged)
	}
	b.noteRemoteResult(false) // 200: backend healthy, reset the failure streak

	uploaded = true
	b.Stats.Puts.Increment()
	span.SetAttributes(attribute.String("cacheprog.label", describeData(raw)))
	return nil
}

// removeClaimed removes a key that was optimistically added to the index
// when the upload fails, so it can be retried on the next attempt.
func (b *WebBackend) removeClaimed(key string) {
	b.keysMu.Lock()
	b.keys.Remove(key)
	b.keysMu.Unlock()
}

// Close drains the batch coalescer and flushes the HTTP error logger.
// The OTel tracer provider is process-wide (see src/trace) and is shut
// down once by the build entrypoint, not per WebBackend — multiple
// components (timeline exporter, cacheprog) share the same provider so
// all spans land in a single OTLP batch.
func (b *WebBackend) Close() error {
	if b.batchStop != nil {
		close(b.batchStop)
		<-b.batchDone
	}
	if b.errLog != nil {
		_ = b.errLog.Close()
	}
	return nil
}

func (b *WebBackend) GetStats() *CacheStats { return &b.Stats }

// signRequest authenticates an HTTP request using HTTP Basic Auth.
func (b *WebBackend) signRequest(req *http.Request) {
	req.SetBasicAuth(b.accessKey, b.secretKey)
}
