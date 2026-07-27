package cache

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wow-look-at-my/go-containers/set"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
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
	Module    string // main module path, stored as object metadata (provenance)
}

// WebBackend stores cache objects in a remote web server with LZ4 compression.
// GETs use the server's batch endpoint to fetch entries with prefetch support,
// proactively populating the local cache with related entries. PUTs are
// coalesced onto the server's /_batch/put endpoint (mirroring the batch GET
// coalescer), falling back to individual PUTs against a server that does not
// support it.
type WebBackend struct {
	client     *http.Client
	bucket     string
	prefix     string
	endpoint   string
	accessKey  string
	secretKey  string
	version    string // go-toolchain version for object metadata
	module     string // main module path for object metadata (provenance)
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
	// indexKeysAtStart is the advertised key count from the startup index
	// fetch, before any Put claims. Reported in WebSummary so the build
	// profile can flag a dead remote (many advertised keys, zero hits).
	indexKeysAtStart int
	missesMu         sync.RWMutex
	knownMiss        set.Set[string] // keys confirmed absent from remote this session

	// Consecutive-empty-batch backoff. An empty-but-200 /_batch/get is a HEALTHY
	// backend that simply has none of this build's keys (a large remote index
	// that does not overlap this build, or a stale/useless one). Without this, a
	// populated-but-non-overlapping index still pays a batch round-trip per cold
	// key. After
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
	// (GET) or a silent drop (PUT). Remote GETs and PUTs are always attempted;
	// the only backpressure handling is the bounded retry that honors the
	// server's Retry-After (see web_resilience.go). A failure that outlasts the
	// retry budget falls back to a local miss for that one operation — the remote
	// tier is never disabled for the rest of the run.
	maxRetries int // bounded retries for transient failures

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

	// Client-side PUT coalescer: each Put preps its object (claim, read,
	// build-id/module-index guard, lz4) then funnels a putReq through
	// putBatchReqCh; the worker collects them on a short time window and
	// ships them as one /_batch/put tar (manifest.json + data/<key>),
	// mirroring the GET coalescer. batchPutUnsupported is set sticky once a
	// server answers /_batch/put with 404/405, after which Put falls back to
	// the per-object doRetryPUT path for the rest of the process.
	putBatchReqCh       chan putReq
	putBatchStop        chan struct{}
	putBatchDone        chan struct{}
	putBatchHTTPWG      sync.WaitGroup
	batchPutUnsupported atomic.Bool
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
				// Rebuild the body via GetBody: orig.Body was consumed by the
				// previous attempt, so re-sending it would transmit an empty
				// body and fail on the ContentLength mismatch. (Every request
				// with a body here is built from a *bytes.Reader, so GetBody
				// is always populated by http.NewRequest.)
				if orig.GetBody != nil {
					body, err := orig.GetBody()
					if err != nil {
						return err
					}
					req.Body = body
					req.GetBody = orig.GetBody
					req.ContentLength = orig.ContentLength
				}
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
		module:    cfg.Module,
	}
	b.tracer = newCacheTracer(os.Stderr)
	b.errLog = newHTTPErrLogger(os.Stderr, httpErrFlushInterval, b.tracer)
	b.batchReqCh = make(chan batchReq, batchReqChBuf)
	b.batchStop = make(chan struct{})
	b.batchDone = make(chan struct{})
	go b.batchCoalescer()
	b.putBatchReqCh = make(chan putReq, batchReqChBuf)
	b.putBatchStop = make(chan struct{})
	b.putBatchDone = make(chan struct{})
	go b.batchPutCoalescer()
	b.keys, b.indexAuthoritative = b.loadOrFetchIndex()
	b.indexEmpty = b.keys.Len() == 0
	b.indexKeysAtStart = b.keys.Len()
	b.knownMiss = set.New[string]()
	if b.indexAuthoritative {
		logger.Info("cacheprog: web index: %d keys", b.keys.Len())
	} else {
		logger.Warn("cacheprog: web index: fetch failed; using %d cached keys (batch probing enabled)", b.keys.Len())
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

// ForgetStale implements staleKeyForgetter: it drops the index claim for
// actionID's key so the next Put re-uploads instead of skipping as known.
// Called by the PUT replace path when a local entry was overwritten because
// its outputID disagreed with a fresh cmd/go PUT — whatever the shared tier
// holds (or is claimed to hold) under that key is not the content cmd/go
// just computed, and only an actual upload can heal it.
func (b *WebBackend) ForgetStale(actionID string) {
	b.removeClaimed(b.key(actionID))
}

// Close drains the batch coalescer and flushes the HTTP error logger.
// The OTel tracer provider is process-wide (see src/trace) and is shut
// down once by the build entrypoint, not per WebBackend — multiple
// components (timeline exporter, cacheprog) share the same provider so
// all spans land in a single OTLP batch.
func (b *WebBackend) Close() error {
	// Flush the PUT coalescer FIRST so every buffered upload is shipped before
	// the backend tears down. A build that ends with objects still in the
	// coalescer would otherwise lose them (they were claimed in the index but
	// never stored remotely). The daemon drains the shared backend exactly here
	// (Daemon.Close → remote.Close, the real WebBackend.Close — the per-connection
	// noCloseBackend suppresses Close), so daemon teardown flushes pending PUTs.
	if b.putBatchStop != nil {
		close(b.putBatchStop)
		<-b.putBatchDone
	}
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
