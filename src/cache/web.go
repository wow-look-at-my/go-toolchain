package cache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	client    *http.Client
	bucket    string
	prefix    string
	endpoint  string
	accessKey string
	secretKey string
	version   string // go-toolchain version for object metadata
	Stats     CacheStats
	Pool      ConcurrencyTracker // HTTP connection pool usage (shared across all Servers)
	Latency   *LatencyStats      // optional; set by Server for sub-operation tracking
	keysMu    sync.RWMutex
	keys      set.Set[string] // known keys, built from ListObjects on startup
	missesMu  sync.RWMutex
	knownMiss set.Set[string] // keys confirmed absent from remote this session

	// OnBatchEntries is called when a batch GET returns prefetch entries.
	// The caller (Server/Daemon) uses this to populate the local cache,
	// turning future remote GETs into local hits.
	OnBatchEntries func(entries []BatchEntry)

	// Miss reason counters for diagnostics.
	MissNotInIndex AtomicCounter
	MissHTTP404    AtomicCounter
	MissHTTPError  AtomicCounter
	MissNoOutputID AtomicCounter
	MissReadBody   AtomicCounter
	MissDecompress AtomicCounter
	MissChecksum   AtomicCounter
	MissBuildID    AtomicCounter
	MissNetwork    AtomicCounter

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
	b.keys = b.loadOrFetchIndex()
	b.knownMiss = set.New[string]()
	fmt.Fprintf(os.Stderr, "cacheprog: web index: %d keys\n", b.keys.Len())
	return b, nil
}

// gbciKeyPrefix is the constant leading portion of every cacheprog cache
// key. The wire format ships only the variable 32-byte action-ID hash that
// follows this prefix; both client and server hardcode the same prefix.
const gbciKeyPrefix = "go-buildcache/v1"

// gbciHashSize is the number of bytes per entry in the index body.
const gbciHashSize = 32

// gbciHeaderSize is the fixed header size in bytes.
const gbciHeaderSize = 24

// gbciVersion is the wire-format version stored in the header.
const gbciVersion = 1

// gbciMagic is the four-byte file-format identifier "GBCI".
var gbciMagic = [4]byte{'G', 'B', 'C', 'I'}

// indexCachePath returns the path for the local on-disk index blob.
// The hash makes the path unique per (endpoint, bucket, prefix) so multiple
// daemons on the same machine targeting different caches don't collide.
func (b *WebBackend) indexCachePath() string {
	h := sha256.Sum256([]byte(b.endpoint + "/" + b.bucket + "/" + b.prefix))
	name := "gocache-web-index-" + hex.EncodeToString(h[:8]) + ".bin"
	return filepath.Join(os.TempDir(), name)
}

// loadOrFetchIndex returns the set of known cache keys for this backend.
// It reads any previously cached blob from disk, then issues a conditional
// GET /<bucket>/_index against the server. On 304 we keep the disk blob;
// on 200 we adopt the new one and persist it. Any failure produces an
// empty set — Get/Put still work, they just always miss b.keys until the
// next refresh.
func (b *WebBackend) loadOrFetchIndex() set.Set[string] {
	path := b.indexCachePath()
	diskBlob, diskKeys, diskETag := b.readDiskIndex(path)

	blob, status, err := b.fetchIndexBlob(diskETag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cacheprog: web index fetch: %v\n", err)
		if diskBlob != nil {
			return diskKeys
		}
		return set.New[string]()
	}
	if status == http.StatusNotModified {
		if diskBlob != nil {
			return diskKeys
		}
		// Server claimed not-modified but we have no disk copy (likely a
		// cleared /tmp between the ETag fetch and now). Refetch unconditionally.
		blob, _, err = b.fetchIndexBlob("")
		if err != nil {
			fmt.Fprintf(os.Stderr, "cacheprog: web index refetch: %v\n", err)
			return set.New[string]()
		}
	}
	keys, _, err := parseIndexBlob(blob)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cacheprog: web index parse: %v\n", err)
		if diskBlob != nil {
			return diskKeys
		}
		return set.New[string]()
	}
	b.writeIndexBlob(path, blob)
	return keys
}

// readDiskIndex returns (raw, parsed, etag) or (nil, empty, "") if the file
// is missing or invalid.
func (b *WebBackend) readDiskIndex(path string) ([]byte, set.Set[string], string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, set.New[string](), ""
	}
	keys, etag, err := parseIndexBlob(data)
	if err != nil {
		return nil, set.New[string](), ""
	}
	return data, keys, etag
}

// fetchIndexBlob does a conditional GET <endpoint>/<bucket>/_index. Returns:
//
//	body, http.StatusOK, nil          for a 200 response
//	nil,  http.StatusNotModified, nil for a 304 response
//	nil,  0, err                      for any transport failure or bad status
func (b *WebBackend) fetchIndexBlob(ifNoneMatch string) ([]byte, int, error) {
	req, err := http.NewRequest("GET", b.endpoint+"/"+b.bucket+"/_index", nil)
	if err != nil {
		return nil, 0, err
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	b.signRequest(req)
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, 0, err
		}
		return body, http.StatusOK, nil
	case http.StatusNotModified:
		return nil, http.StatusNotModified, nil
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, 0, fmt.Errorf("HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
}

// writeIndexBlob persists a GBCI v1 blob via tmp file + atomic rename.
// Best-effort: failures are silently ignored, since a missing or stale on-disk
// cache only forces the next start to do a fresh GET, never affects correctness.
func (b *WebBackend) writeIndexBlob(path string, blob []byte) {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, blob, 0644); err != nil {
		return
	}
	os.Rename(tmp, path)
}

// parseIndexBlob validates a GBCI v1 blob and returns:
//
//   - the reconstructed key set (full S3 key strings)
//   - the strong ETag (hex-encoded SHA-256 trailer, RFC-7232 quoted)
//   - or an error if magic, version, length, or trailer hash don't validate.
func parseIndexBlob(blob []byte) (set.Set[string], string, error) {
	if len(blob) < gbciHeaderSize+sha256.Size {
		return set.Set[string]{}, "", fmt.Errorf("blob too small (%d bytes)", len(blob))
	}
	if !bytes.Equal(blob[0:4], gbciMagic[:]) {
		return set.Set[string]{}, "", fmt.Errorf("bad magic")
	}
	if blob[4] != gbciVersion {
		return set.Set[string]{}, "", fmt.Errorf("unsupported version %d", blob[4])
	}
	if blob[5] != gbciHashSize {
		return set.Set[string]{}, "", fmt.Errorf("unsupported hash size %d", blob[5])
	}
	count := binary.LittleEndian.Uint64(blob[16:24])
	bodyEnd := gbciHeaderSize + int(count)*gbciHashSize
	if bodyEnd+sha256.Size != len(blob) {
		return set.Set[string]{}, "", fmt.Errorf("length %d != header+%d*%d+trailer", len(blob), count, gbciHashSize)
	}
	expected := sha256.Sum256(blob[:bodyEnd])
	if !bytes.Equal(expected[:], blob[bodyEnd:]) {
		return set.Set[string]{}, "", fmt.Errorf("trailer hash mismatch")
	}
	keys := set.New[string]()
	hashHex := make([]byte, gbciHashSize*2)
	for i := uint64(0); i < count; i++ {
		off := gbciHeaderSize + int(i)*gbciHashSize
		hex.Encode(hashHex, blob[off:off+gbciHashSize])
		keys.Add(gbciKeyPrefix + string(hashHex))
	}
	etag := `"` + hex.EncodeToString(blob[bodyEnd:]) + `"`
	return keys, etag, nil
}

// marshalIndex encodes the given key set as a GBCI v1 blob. Keys not matching
// the cacheprog pattern are skipped. Used by tests.
func marshalIndex(keys set.Set[string]) []byte {
	hashes := make([][gbciHashSize]byte, 0, keys.Len())
	for k := range keys.All() {
		if h, ok := decodeActionHash(k); ok {
			hashes = append(hashes, h)
		}
	}
	sort.Slice(hashes, func(i, j int) bool {
		return bytes.Compare(hashes[i][:], hashes[j][:]) < 0
	})
	blob := make([]byte, gbciHeaderSize+len(hashes)*gbciHashSize+sha256.Size)
	copy(blob[0:4], gbciMagic[:])
	blob[4] = gbciVersion
	blob[5] = gbciHashSize
	binary.LittleEndian.PutUint16(blob[6:8], 0)
	binary.LittleEndian.PutUint64(blob[8:16], 0)
	binary.LittleEndian.PutUint64(blob[16:24], uint64(len(hashes)))
	off := gbciHeaderSize
	for i := range hashes {
		copy(blob[off:off+gbciHashSize], hashes[i][:])
		off += gbciHashSize
	}
	digest := sha256.Sum256(blob[:off])
	copy(blob[off:], digest[:])
	return blob
}

// decodeActionHash extracts the 32-byte action ID from a cacheprog cache key.
func decodeActionHash(key string) ([gbciHashSize]byte, bool) {
	var zero [gbciHashSize]byte
	if !strings.HasPrefix(key, gbciKeyPrefix) {
		return zero, false
	}
	hex64 := key[len(gbciKeyPrefix):]
	if len(hex64) != gbciHashSize*2 {
		return zero, false
	}
	var h [gbciHashSize]byte
	if _, err := hex.Decode(h[:], []byte(hex64)); err != nil {
		return zero, false
	}
	return h, true
}

func (b *WebBackend) key(actionID string) string {
	return b.prefix + "v1" + actionID
}

func (b *WebBackend) url(key string) string {
	return b.endpoint + "/" + b.bucket + "/" + key
}

// Get retrieves a cached object. If the key is in the local index, it fetches
// individually. Otherwise, it uses the server's batch GET endpoint with
// prefetch enabled — the server returns the requested entry plus temporally
// related entries from the same build, which are passed to OnBatchEntries
// for local cache population.
func (b *WebBackend) Get(actionID string) (outputID string, body io.ReadCloser, size int64, t time.Time, miss bool, err error) {
	key := b.key(actionID)
	b.keysMu.RLock()
	known := b.keys.Contains(key)
	b.keysMu.RUnlock()
	if known {
		return b.getIndividual(nil, actionID, key)
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
	return b.getBatch(actionID, key)
}

// getIndividual fetches a single object stored as an individual S3 key.
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
	resp, err := b.client.Do(req)
	if err != nil {
		b.Pool.Release()
		b.MissNetwork.Increment()
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
		markSpanMiss(span, "http_404")
		return "", nil, 0, time.Time{}, true, nil
	}
	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		b.Pool.Release()
		b.MissHTTPError.Increment()
		markSpanMiss(span, fmt.Sprintf("http_%d", resp.StatusCode))
		b.errLog.Record("web get", resp.StatusCode, actionID, string(respBody))
		return "", nil, 0, time.Time{}, true, nil
	}

	outputID := resp.Header.Get("X-Amz-Meta-Outputid")
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
	// corrupt — truncated, badly decoded, or poisoned/rotted in S3 — and
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
	key := b.key(actionID)

	// Atomically check-and-claim: if the key is already known (or being
	// uploaded by another goroutine), skip immediately.
	b.keysMu.Lock()
	if b.keys.Contains(key) {
		b.keysMu.Unlock()
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
		markSpanMiss(span, "buildid_mismatch")
		fmt.Fprintf(os.Stderr, "cacheprog: web put %s: refusing upload, build-id action mismatch (want action=%s, got action=%s); object does not belong under this key\n",
			shortID(actionID), expectedBuildIDAction(actionID), act)
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
	req.Header.Set("X-Amz-Meta-Outputid", outputID)
	req.Header.Set("X-Amz-Meta-Object-Type", detectObjectType(raw))
	req.Header.Set("X-Amz-Meta-Body-Size", strconv.FormatInt(bodySize, 10))
	req.Header.Set("X-Amz-Meta-Compression", "lz4")
	req.Header.Set("X-Amz-Meta-Created", time.Now().UTC().Format(time.RFC3339))
	if b.version != "" {
		req.Header.Set("X-Amz-Meta-Toolchain-Version", b.version)
	}
	if goVer, target := parseArchiveHeader(raw); goVer != "" {
		req.Header.Set("X-Amz-Meta-Go-Version", goVer)
		req.Header.Set("X-Amz-Meta-Target", target)
	}
	if pkg := parseImportPath(raw); pkg != "" {
		req.Header.Set("X-Amz-Meta-Pkg", pkg)
	}
	if files := parseSourceFiles(raw); len(files) > 0 {
		req.Header.Set("X-Amz-Meta-Src", strings.Join(files, " "))
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
		span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d", resp.StatusCode))
		b.errLog.Record("web put", resp.StatusCode, actionID, string(respBody))
		return fmt.Errorf("web put: HTTP %d: %w", resp.StatusCode, errLogged)
	}

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
