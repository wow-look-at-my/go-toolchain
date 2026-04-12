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

	"github.com/pierrec/lz4/v4"
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

// Get retrieves a cached object. The returned body is decompressed.
// Returns immediately if the key is not in the index (no network call).
func (b *WebBackend) Get(actionID string) (outputID string, body io.ReadCloser, size int64, t time.Time, miss bool, err error) {
	key := b.key(actionID)
	b.keysMu.RLock()
	known := b.keys.Contains(key)
	b.keysMu.RUnlock()
	if !known {
		return "", nil, 0, time.Time{}, true, nil
	}
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

	outputID = resp.Header.Get("X-Amz-Meta-Outputid")
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

	t = time.Now()
	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		if parsed, parseErr := time.Parse(http.TimeFormat, lm); parseErr == nil {
			t = parsed
		}
	}

	b.Stats.Hits.Increment()
	return outputID, io.NopCloser(bytes.NewReader(decompressed)), int64(len(decompressed)), t, false, nil
}

// Put stores a cached object with LZ4 compression.
// Skips upload if the key is already in the index.
func (b *WebBackend) Put(actionID, outputID string, body io.Reader, bodySize int64) error {
	key := b.key(actionID)

	// Atomically check-and-claim: if the key is already known (or being
	// uploaded by another goroutine), skip immediately. Otherwise mark it
	// as claimed so concurrent Puts for the same actionID don't race.
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

// Close is a no-op for the web backend.
func (b *WebBackend) Close() error { return nil }

func (b *WebBackend) GetStats() *CacheStats { return &b.Stats }

// signRequest authenticates an HTTP request using HTTP Basic Auth.
func (b *WebBackend) signRequest(req *http.Request) {
	req.SetBasicAuth(b.accessKey, b.secretKey)
}

// detectObjectType identifies the type of a cache entry from its magic bytes.
func detectObjectType(data []byte) string {
	if len(data) >= 8 && string(data[:8]) == "!<arch>\n" {
		return "go-archive"
	}
	if len(data) >= 4 && data[0] == 0x7f && data[1] == 'E' && data[2] == 'L' && data[3] == 'F' {
		return "elf-binary"
	}
	if len(data) >= 4 {
		// Mach-O 64-bit (little-endian and big-endian) and 32-bit.
		m := uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
		switch m {
		case 0xcffaedfe, 0xfeedface, 0xfeedfacf, 0xcefaedfe, 0xcafebabe:
			return "macho-binary"
		}
	}
	if len(data) >= 2 && data[0] == 'M' && data[1] == 'Z' {
		return "pe-binary"
	}
	if len(data) >= 4 && data[0] == 0x00 && data[1] == 'g' && data[2] == 'o' && data[3] == '1' {
		return "go-object"
	}
	return "unknown"
}

// parseArchiveHeader scans a Go archive for the "go object" line inside
// __.PKGDEF. Returns Go version and target (GOOS/GOARCH), or empty strings
// if not found. Only scans the first 1024 bytes.
func parseArchiveHeader(data []byte) (goVersion, target string) {
	limit := 1024
	if len(data) < limit {
		limit = len(data)
	}
	window := data[:limit]
	// Look for a line starting with "go object ".
	const prefix = "go object "
	for len(window) > 0 {
		idx := bytes.Index(window, []byte(prefix))
		if idx < 0 {
			break
		}
		// Ensure it's at the start of a line (idx == 0 or preceded by newline).
		if idx > 0 && window[idx-1] != '\n' {
			window = window[idx+len(prefix):]
			continue
		}
		line := window[idx:]
		if nl := bytes.IndexByte(line, '\n'); nl >= 0 {
			line = line[:nl]
		}
		// Format: "go object <GOOS> <GOARCH> <goversion> [experiments...]"
		fields := strings.Fields(string(line))
		if len(fields) >= 5 {
			return fields[4], fields[2] + "/" + fields[3]
		}
		break
	}
	return "", ""
}

func compressData(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := lz4.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decompressData(data []byte) ([]byte, error) {
	r := lz4.NewReader(bytes.NewReader(data))
	return io.ReadAll(r)
}
