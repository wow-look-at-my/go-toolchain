package cache

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/xml"
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
// Small entries (< 64 KB) are batched into tar+lz4 archives to reduce the
// number of HTTP requests. Large entries are uploaded individually.
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
	keys    set.Set[string] // known individual keys, built from ListObjects on startup

	// Batch write buffer. batchPending tracks actionIDs currently in
	// batchBuf for fast dedup without scanning the slice.
	batchMu      sync.Mutex
	batchBuf     []batchEntry
	batchBufSize int64
	batchTimer   *time.Timer
	batchPending set.Set[string]

	// Batch index: actionID → batch location. Loaded from remote on startup
	// and updated in-memory as new batches are flushed.
	batchIndexMu sync.RWMutex
	batchIndex   map[string]batchIndexEntry

	// Local directory for caching downloaded batch archives.
	batchCacheDir string

	// OnBatchEntries is called when a batch is downloaded for a GET.
	// It receives all extracted entries so the caller (Server) can
	// proactively populate the local cache, turning N remote GETs
	// into 1 batch download + (N-1) local hits.
	OnBatchEntries func(entries []extractedEntry)

	// OnBatchFlush is called after a batch is successfully uploaded.
	// The argument is the number of entries in the flushed batch.
	OnBatchFlush func(entries int)
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
	b.keys = b.loadOrFetchIndex()
	b.batchIndex = b.loadBatchIndex()
	b.batchPending = set.New[string]()

	h := sha256.Sum256([]byte(b.endpoint + "/" + b.bucket + "/" + b.prefix))
	b.batchCacheDir = filepath.Join(os.TempDir(), "gocache-batches-"+hex.EncodeToString(h[:8]))
	os.MkdirAll(b.batchCacheDir, 0o755)

	fmt.Fprintf(os.Stderr, "cacheprog: web index: %d keys, %d batched\n", b.keys.Len(), len(b.batchIndex))
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

// Get retrieves a cached object. It checks individual keys first, then the
// batch index. Returns immediately if the key is not in either index.
func (b *WebBackend) Get(actionID string) (outputID string, body io.ReadCloser, size int64, t time.Time, miss bool, err error) {
	// Try individual key first.
	key := b.key(actionID)
	b.keysMu.RLock()
	known := b.keys.Contains(key)
	b.keysMu.RUnlock()
	if known {
		return b.getIndividual(actionID, key)
	}

	// Try batch index.
	b.batchIndexMu.RLock()
	entry, inBatch := b.batchIndex[actionID]
	b.batchIndexMu.RUnlock()
	if inBatch {
		return b.getFromBatch(actionID, entry)
	}

	return "", nil, 0, time.Time{}, true, nil
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
		fmt.Fprintf(os.Stderr, "cacheprog: web get %s: %v\n", actionID[:8], err)
		return "", nil, 0, time.Time{}, true, nil
	}

	if resp.StatusCode == 404 {
		resp.Body.Close()
		b.Pool.Release()
		return "", nil, 0, time.Time{}, true, nil
	}
	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		b.Pool.Release()
		fmt.Fprintf(os.Stderr, "cacheprog: web get %s: HTTP %d: %s\n", actionID[:8], resp.StatusCode, respBody)
		return "", nil, 0, time.Time{}, true, nil
	}

	outputID := resp.Header.Get("X-Amz-Meta-Outputid")
	if outputID == "" {
		resp.Body.Close()
		b.Pool.Release()
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
		fmt.Fprintf(os.Stderr, "cacheprog: web get %s: read body: %v\n", actionID[:8], err)
		return "", nil, 0, time.Time{}, true, nil
	}

	decompressStart := time.Now()
	decompressed, err := decompressData(compressed)
	if b.Latency != nil {
		b.Latency.Decompress.Record(time.Since(decompressStart))
	}
	if err != nil {
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

// getFromBatch retrieves a cache entry from a batch archive. When a batch
// is downloaded for the first time, ALL entries are extracted and passed to
// OnBatchEntries so the caller can populate the local cache proactively.
// This turns N sequential remote GETs into 1 batch download + (N-1) local hits.
func (b *WebBackend) getFromBatch(actionID string, entry batchIndexEntry) (string, io.ReadCloser, int64, time.Time, bool, error) {
	start := time.Now()
	batchData, err := b.loadBatch(entry.Batch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cacheprog: web batch get %s: %v\n", actionID[:8], err)
		return "", nil, 0, time.Time{}, true, nil
	}

	// Extract ALL entries and proactively populate the local cache.
	all, err := extractAllFromBatch(batchData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cacheprog: web batch extract %s: %v\n", actionID[:8], err)
		return "", nil, 0, time.Time{}, true, nil
	}

	if b.OnBatchEntries != nil {
		b.OnBatchEntries(all)
	}

	fmt.Fprintf(os.Stderr, "cacheprog: batch get %s: fetched %s (%d entries, %d bytes) in %v\n",
		actionID[:8], entry.Batch, len(all), len(batchData), time.Since(start).Round(time.Millisecond))

	// Find the requested entry in the extracted results.
	for _, e := range all {
		if e.ActionID == actionID {
			b.Stats.Hits.Increment()
			return e.OutputID, io.NopCloser(bytes.NewReader(e.Data)), int64(len(e.Data)), time.Now(), false, nil
		}
	}

	fmt.Fprintf(os.Stderr, "cacheprog: web batch extract %s: entry not in batch\n", actionID[:8])
	return "", nil, 0, time.Time{}, true, nil
}

// loadBatch returns the raw bytes of a batch archive, using the local cache
// or downloading from the remote server.
func (b *WebBackend) loadBatch(name string) ([]byte, error) {
	// Try local cache first.
	localPath := filepath.Join(b.batchCacheDir, name)
	if data, err := os.ReadFile(localPath); err == nil {
		return data, nil
	}

	// Download from remote.
	batchKey := b.prefix + "batches/" + name
	req, err := http.NewRequest("GET", b.url(batchKey), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	b.signRequest(req)

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	// Cache locally for future reads.
	os.WriteFile(localPath, data, 0o644)

	return data, nil
}

// Put stores a cached object. Small entries (< 64 KB) are buffered and
// uploaded as a batch archive. Large entries are uploaded individually with
// LZ4 compression. Skips if the actionID is already known.
func (b *WebBackend) Put(actionID, outputID string, body io.Reader, bodySize int64) error {
	key := b.key(actionID)

	// Dedup: check individual keys.
	b.keysMu.RLock()
	if b.keys.Contains(key) {
		b.keysMu.RUnlock()
		return nil
	}
	b.keysMu.RUnlock()

	// Dedup: check flushed batch index.
	b.batchIndexMu.RLock()
	_, inBatch := b.batchIndex[actionID]
	b.batchIndexMu.RUnlock()
	if inBatch {
		return nil
	}

	// Dedup: check pending batch buffer.
	b.batchMu.Lock()
	if b.batchPending.Contains(actionID) {
		b.batchMu.Unlock()
		return nil
	}
	b.batchMu.Unlock()

	raw, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("web put read: %w", err)
	}

	// Small entries: buffer for batch upload.
	if int64(len(raw)) < batchSizeThreshold {
		b.addToBatch(batchEntry{actionID: actionID, outputID: outputID, data: raw})
		return nil
	}

	// Large entries: claim key and upload individually.
	b.keysMu.Lock()
	if b.keys.Contains(key) {
		b.keysMu.Unlock()
		return nil
	}
	b.keys.Add(key)
	b.keysMu.Unlock()

	return b.putIndividual(actionID, outputID, key, raw, bodySize)
}

// putIndividual uploads a single object with LZ4 compression and metadata headers.
func (b *WebBackend) putIndividual(actionID, outputID, key string, raw []byte, bodySize int64) error {
	var uploaded bool
	defer func() {
		if !uploaded {
			b.removeClaimed(key)
		}
	}()

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
		fmt.Fprintf(os.Stderr, "cacheprog: web put %s: HTTP %d: %s\n", actionID[:8], resp.StatusCode, respBody)
		return fmt.Errorf("web put: HTTP %d", resp.StatusCode)
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

// Close flushes any buffered batch entries and releases resources.
func (b *WebBackend) Close() error {
	b.stopBatchTimer()
	b.flushBatch()
	return nil
}

func (b *WebBackend) GetStats() *CacheStats { return &b.Stats }

// addToBatch appends an entry to the write buffer and triggers a flush if
// the buffer exceeds size or count thresholds.
func (b *WebBackend) addToBatch(e batchEntry) {
	b.batchMu.Lock()
	b.batchPending.Add(e.actionID)
	b.batchBuf = append(b.batchBuf, e)
	b.batchBufSize += int64(len(e.data))
	shouldFlush := b.batchBufSize >= batchFlushBytes || len(b.batchBuf) >= batchFlushCount
	firstEntry := len(b.batchBuf) == 1
	b.batchMu.Unlock()

	if shouldFlush {
		b.flushBatch()
	} else if firstEntry {
		b.startBatchTimer()
	}
}

// flushBatch creates a tar+lz4 archive from buffered entries, uploads it,
// and updates the remote batch index.
func (b *WebBackend) flushBatch() {
	b.stopBatchTimer()

	b.batchMu.Lock()
	pending := b.batchBuf
	b.batchBuf = nil
	b.batchBufSize = 0
	b.batchPending = set.New[string]()
	b.batchMu.Unlock()

	if len(pending) == 0 {
		return
	}

	data, manifest, err := createBatch(pending)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cacheprog: batch create: %v\n", err)
		b.removeClaimedBatch(pending)
		return
	}

	name := batchName()
	batchKey := b.prefix + "batches/" + name
	if err := b.uploadObject(batchKey, data); err != nil {
		fmt.Fprintf(os.Stderr, "cacheprog: batch upload: %v\n", err)
		b.removeClaimedBatch(pending)
		return
	}

	// Update in-memory batch index.
	b.batchIndexMu.Lock()
	for _, e := range manifest.Entries {
		b.batchIndex[e.ActionID] = batchIndexEntry{
			Batch:    name,
			OutputID: e.OutputID,
			Size:     e.Size,
		}
	}
	indexCopy := make(map[string]batchIndexEntry, len(b.batchIndex))
	for k, v := range b.batchIndex {
		indexCopy[k] = v
	}
	b.batchIndexMu.Unlock()

	// Persist merged index to remote (best-effort).
	b.uploadBatchIndex(indexCopy)

	// Cache the batch locally for future reads.
	localPath := filepath.Join(b.batchCacheDir, name)
	os.WriteFile(localPath, data, 0o644)

	b.Stats.Puts.Add(uint32(len(pending)))
	if b.OnBatchFlush != nil {
		b.OnBatchFlush(len(pending))
	}
	fmt.Fprintf(os.Stderr, "cacheprog: batch flushed %d entries (%d bytes compressed)\n", len(pending), len(data))
}

// removeClaimedBatch is a no-op since batched entries don't claim individual
// keys. The batchPending set is already cleared by flushBatch, so on failure
// the entries can be re-submitted on the next build.
func (b *WebBackend) removeClaimedBatch(_ []batchEntry) {}

func (b *WebBackend) startBatchTimer() {
	b.batchMu.Lock()
	defer b.batchMu.Unlock()
	if b.batchTimer != nil {
		return
	}
	b.batchTimer = time.AfterFunc(batchFlushInterval, func() {
		b.flushBatch()
	})
}

func (b *WebBackend) stopBatchTimer() {
	b.batchMu.Lock()
	defer b.batchMu.Unlock()
	if b.batchTimer != nil {
		b.batchTimer.Stop()
		b.batchTimer = nil
	}
}

// signRequest authenticates an HTTP request using HTTP Basic Auth.
func (b *WebBackend) signRequest(req *http.Request) {
	req.SetBasicAuth(b.accessKey, b.secretKey)
}
