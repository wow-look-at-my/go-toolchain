package cache

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wow-look-at-my/go-containers/set"
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
	Stats   CacheStats
	Pool    ConcurrencyTracker // HTTP connection pool usage (shared across all Servers)
	Latency *LatencyStats      // optional; set by Server for sub-operation tracking
	keysMu  sync.RWMutex
	keys    set.Set[string] // known keys, built from ListObjects on startup

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
	MissNetwork    AtomicCounter

	errLog *httpErrLogger
}

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
		TLSHandshakeTimeout:  10 * time.Second,
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
	b.errLog = newHTTPErrLogger(os.Stderr, httpErrFlushInterval)
	b.keys = b.loadOrFetchIndex()
	fmt.Fprintf(os.Stderr, "cacheprog: web index: %d keys\n", b.keys.Len())
	return b, nil
}

// indexCacheTTL is the maximum age of a cached index file before re-fetching.
const indexCacheTTL = 5 * time.Minute

// indexCachePath returns the path for the local index cache file.
func (b *WebBackend) indexCachePath() string {
	h := sha256.Sum256([]byte(b.endpoint + "/" + b.bucket + "/" + b.prefix))
	name := "gocache-web-index-" + hex.EncodeToString(h[:8]) + ".txt"
	return filepath.Join(os.TempDir(), name)
}

// loadOrFetchIndex tries to load the key index from a local cache file.
// If the file is missing or stale, it fetches from the server and persists the result.
func (b *WebBackend) loadOrFetchIndex() set.Set[string] {
	path := b.indexCachePath()
	if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) < indexCacheTTL {
		if keys, err := b.readIndexFile(path); err == nil {
			return keys
		}
	}
	keys := b.listAllKeys()
	b.writeIndexFile(path, keys)
	return keys
}

// readIndexFile reads a newline-delimited key list from disk.
func (b *WebBackend) readIndexFile(path string) (set.Set[string], error) {
	f, err := os.Open(path)
	if err != nil {
		return set.Set[string]{}, err
	}
	defer f.Close()
	keys := set.New[string]()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if line := scanner.Text(); line != "" {
			keys.Add(line)
		}
	}
	return keys, scanner.Err()
}

// writeIndexFile persists the key index as a newline-delimited file.
func (b *WebBackend) writeIndexFile(path string, keys set.Set[string]) {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return
	}
	w := bufio.NewWriter(f)
	for k := range keys.All() {
		fmt.Fprintln(w, k)
	}
	w.Flush()
	f.Close()
	os.Rename(tmp, path)
}

// listObjectsResult is the XML response from ListObjectsV2.
type listObjectsResult struct {
	XMLName               xml.Name `xml:"ListBucketResult"`
	Contents              []struct{ Key string } `xml:"Contents"`
	IsTruncated           bool     `xml:"IsTruncated"`
	NextContinuationToken string   `xml:"NextContinuationToken"`
}

// listAllKeys fetches all keys with our prefix using ListObjectsV2.
func (b *WebBackend) listAllKeys() set.Set[string] {
	keys := set.New[string]()
	continuation := ""
	for {
		query := "list-type=2&prefix=" + url.QueryEscape(b.prefix) + "&max-keys=1000"
		if continuation != "" {
			query += "&continuation-token=" + url.QueryEscape(continuation)
		}
		url := b.endpoint + "/" + b.bucket + "?" + query
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			break
		}
		b.signRequest(req)
		resp, err := b.client.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cacheprog: web list: %v\n", err)
			break
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			fmt.Fprintf(os.Stderr, "cacheprog: web list: HTTP %d: %s\n", resp.StatusCode, body)
			break
		}
		var result listObjectsResult
		if err := xml.Unmarshal(body, &result); err != nil {
			fmt.Fprintf(os.Stderr, "cacheprog: web list: xml parse: %v\n", err)
			break
		}
		for _, c := range result.Contents {
			keys.Add(c.Key)
		}
		if !result.IsTruncated {
			break
		}
		continuation = result.NextContinuationToken
	}
	return keys
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
		return b.getIndividual(actionID, key)
	}

	// Key not in index — try batch GET with prefetch.
	b.MissNotInIndex.Increment()
	return b.getBatch(actionID, key)
}

// getIndividual fetches a single object stored as an individual S3 key.
func (b *WebBackend) getIndividual(actionID, key string) (string, io.ReadCloser, int64, time.Time, bool, error) {
	req, err := http.NewRequest("GET", b.url(key), nil)
	if err != nil {
		return "", nil, 0, time.Time{}, true, nil
	}
	b.signRequest(req)

	b.Pool.Acquire()
	httpStart := time.Now()
	resp, err := b.client.Do(req)
	if err != nil {
		b.Pool.Release()
		b.MissNetwork.Increment()
		fmt.Fprintf(os.Stderr, "cacheprog: web get %s: %v\n", actionID[:8], err)
		return "", nil, 0, time.Time{}, true, nil
	}

	if resp.StatusCode == 404 {
		resp.Body.Close()
		b.Pool.Release()
		b.MissHTTP404.Increment()
		return "", nil, 0, time.Time{}, true, nil
	}
	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		b.Pool.Release()
		b.MissHTTPError.Increment()
		b.errLog.Record("web get", resp.StatusCode, actionID, string(respBody))
		return "", nil, 0, time.Time{}, true, nil
	}

	outputID := resp.Header.Get("X-Amz-Meta-Outputid")
	if outputID == "" {
		resp.Body.Close()
		b.Pool.Release()
		b.MissNoOutputID.Increment()
		fmt.Fprintf(os.Stderr, "cacheprog: web get %s: missing outputid metadata\n", actionID[:8])
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
		fmt.Fprintf(os.Stderr, "cacheprog: web get %s: read body: %v\n", actionID[:8], err)
		return "", nil, 0, time.Time{}, true, nil
	}

	decompressStart := time.Now()
	decompressed, err := decompressData(compressed)
	if b.Latency != nil {
		b.Latency.Decompress.Record(time.Since(decompressStart))
	}
	if err != nil {
		b.MissDecompress.Increment()
		fmt.Fprintf(os.Stderr, "cacheprog: web get %s: decompress: %v\n", actionID[:8], err)
		return "", nil, 0, time.Time{}, true, nil
	}

	t := time.Now()
	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		if parsed, parseErr := time.Parse(http.TimeFormat, lm); parseErr == nil {
			t = parsed
		}
	}

	b.Stats.Hits.Increment()
	return outputID, io.NopCloser(bytes.NewReader(decompressed)), int64(len(decompressed)), t, false, nil
}

// getBatch uses the server's batch GET endpoint to fetch an entry along with
// prefetched related entries. The server assembles the response on the fly
// using temporal locality — entries uploaded around the same time are included.
// Prefetched entries are passed to OnBatchEntries for local cache population.
func (b *WebBackend) getBatch(actionID, key string) (string, io.ReadCloser, int64, time.Time, bool, error) {
	start := time.Now()

	reqBody, _ := json.Marshal(batchGetRequest{
		Keys:     []string{key},
		Prefetch: true,
	})

	batchURL := b.endpoint + "/" + b.bucket + "/_batch/get"
	req, err := http.NewRequest("GET", batchURL, bytes.NewReader(reqBody))
	if err != nil {
		return "", nil, 0, time.Time{}, true, nil
	}
	req.Header.Set("Content-Type", "application/json")
	b.signRequest(req)

	b.Pool.Acquire()
	resp, err := b.client.Do(req)
	if err != nil {
		b.Pool.Release()
		fmt.Fprintf(os.Stderr, "cacheprog: web batch get %s: %v\n", actionID[:8], err)
		return "", nil, 0, time.Time{}, true, nil
	}

	if resp.StatusCode != 200 {
		resp.Body.Close()
		b.Pool.Release()
		// Fall back to individual GET if batch endpoint isn't available.
		if resp.StatusCode == 404 || resp.StatusCode == 405 {
			return b.getIndividual(actionID, key)
		}
		b.errLog.Record("web batch get", resp.StatusCode, actionID, "")
		return "", nil, 0, time.Time{}, true, nil
	}

	entries, err := parseBatchResponse(resp.Body)
	resp.Body.Close()
	b.Pool.Release()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cacheprog: web batch get %s: parse: %v\n", actionID[:8], err)
		return "", nil, 0, time.Time{}, true, nil
	}

	// Pass prefetched entries to the callback for local cache population.
	if b.OnBatchEntries != nil && len(entries) > 1 {
		b.OnBatchEntries(entries)
	}

	var nPrefetch int
	for _, e := range entries {
		if e.Prefetch {
			nPrefetch++
		}
	}
	b.errLog.RecordBatchInfo(actionID, len(entries), nPrefetch, time.Since(start))

	// Find the requested entry and decompress it.
	for _, e := range entries {
		if e.Key == key {
			decompressed, err := decompressData(e.Data)
			if err != nil {
				fmt.Fprintf(os.Stderr, "cacheprog: web batch get %s: decompress: %v\n", actionID[:8], err)
				return "", nil, 0, time.Time{}, true, nil
			}
			b.Stats.Hits.Increment()
			return e.OutputID, io.NopCloser(bytes.NewReader(decompressed)), int64(len(decompressed)), time.Now(), false, nil
		}
	}

	return "", nil, 0, time.Time{}, true, nil
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

	var uploaded bool
	defer func() {
		if !uploaded {
			b.removeClaimed(key)
		}
	}()

	raw, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("web put read: %w", err)
	}

	compressStart := time.Now()
	compressed, err := compressData(raw)
	if b.Latency != nil {
		b.Latency.Compress.Record(time.Since(compressStart))
	}
	if err != nil {
		return fmt.Errorf("web put compress: %w", err)
	}

	req, err := http.NewRequest("PUT", b.url(key), bytes.NewReader(compressed))
	if err != nil {
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
	b.signRequest(req)

	b.Pool.Acquire()
	httpStart := time.Now()
	resp, err := b.client.Do(req)
	b.Pool.Release()
	if b.Latency != nil && err == nil {
		b.Latency.HTTPPut.Record(time.Since(httpStart))
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "cacheprog: web put %s: %v\n", actionID[:8], err)
		return err
	}
	// Drain and close body so the connection is returned to the pool.
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		b.errLog.Record("web put", resp.StatusCode, actionID, string(respBody))
		return fmt.Errorf("web put: HTTP %d: %w", resp.StatusCode, errLogged)
	}

	uploaded = true
	b.Stats.Puts.Increment()
	return nil
}

// removeClaimed removes a key that was optimistically added to the index
// when the upload fails, so it can be retried on the next attempt.
func (b *WebBackend) removeClaimed(key string) {
	b.keysMu.Lock()
	b.keys.Remove(key)
	b.keysMu.Unlock()
}

// Close flushes the HTTP error logger and shuts down the OTel exporter
// (if one was started). It is safe to call on a partially-constructed
// WebBackend (e.g. in tests that build &WebBackend{} bare).
func (b *WebBackend) Close() error {
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
