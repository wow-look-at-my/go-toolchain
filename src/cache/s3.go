package cache

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
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

// S3Config holds the configuration for an S3-compatible backend.
type S3Config struct {
	Bucket    string // Required. Empty bucket disables the backend.
	Endpoint  string // S3 endpoint URL (e.g. "https://s3.pazer.io"). Required.
	Prefix    string // Key prefix (defaults to "go-buildcache/").
	AccessKey string // S3 access key ID (used as Basic Auth username)
	SecretKey string // S3 secret access key (used as Basic Auth password)
	Version   string // go-toolchain version, stored as object metadata
}

// S3Backend stores cache objects in an S3-compatible bucket with LZ4 compression.
type S3Backend struct {
	client    *http.Client
	bucket    string
	prefix    string
	endpoint  string
	accessKey string
	secretKey string
	version   string // go-toolchain version for object metadata
	Stats  CacheStats
	keysMu sync.RWMutex
	keys   set.Set[string] // known keys, built from ListObjects on startup
}

// NewS3Backend creates an S3 backend from the given config.
// Returns nil if bucket is empty.
func NewS3Backend(cfg S3Config) (*S3Backend, error) {
	if cfg.Bucket == "" {
		return nil, nil
	}
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("s3: endpoint is required")
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
		return nil, fmt.Errorf("s3: access key and secret key are required")
	}

	endpoint := strings.TrimRight(cfg.Endpoint, "/")
	if !strings.HasPrefix(endpoint, "https://") && !strings.HasPrefix(endpoint, "http://") {
		endpoint = "https://" + endpoint
	}

	b := &S3Backend{
		client:    &http.Client{Timeout: 30 * time.Second},
		bucket:    cfg.Bucket,
		prefix:    prefix,
		endpoint:  endpoint,
		accessKey: accessKey,
		secretKey: secretKey,
		version:   cfg.Version,
	}
	b.keys = b.loadOrFetchIndex()
	fmt.Fprintf(os.Stderr, "cacheprog: s3 index: %d keys\n", b.keys.Len())
	return b, nil
}

// indexCacheTTL is the maximum age of a cached index file before re-fetching.
const indexCacheTTL = 5 * time.Minute

// indexCachePath returns the path for the local index cache file.
func (b *S3Backend) indexCachePath() string {
	h := sha256.Sum256([]byte(b.endpoint + "/" + b.bucket + "/" + b.prefix))
	name := "gocache-s3-index-" + hex.EncodeToString(h[:8]) + ".txt"
	return filepath.Join(os.TempDir(), name)
}

// loadOrFetchIndex tries to load the key index from a local cache file.
// If the file is missing or stale, it fetches from S3 and persists the result.
func (b *S3Backend) loadOrFetchIndex() set.Set[string] {
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
func (b *S3Backend) readIndexFile(path string) (set.Set[string], error) {
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
func (b *S3Backend) writeIndexFile(path string, keys set.Set[string]) {
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

// listObjectsResult is the XML response from S3 ListObjectsV2.
type listObjectsResult struct {
	XMLName               xml.Name `xml:"ListBucketResult"`
	Contents              []struct{ Key string } `xml:"Contents"`
	IsTruncated           bool     `xml:"IsTruncated"`
	NextContinuationToken string   `xml:"NextContinuationToken"`
}

// listAllKeys fetches all keys with our prefix from S3 using ListObjectsV2.
func (b *S3Backend) listAllKeys() set.Set[string] {
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
			fmt.Fprintf(os.Stderr, "cacheprog: s3 list: %v\n", err)
			break
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			fmt.Fprintf(os.Stderr, "cacheprog: s3 list: HTTP %d: %s\n", resp.StatusCode, body)
			break
		}
		var result listObjectsResult
		if err := xml.Unmarshal(body, &result); err != nil {
			fmt.Fprintf(os.Stderr, "cacheprog: s3 list: xml parse: %v\n", err)
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

func (b *S3Backend) key(actionID string) string {
	return b.prefix + "v1" + actionID
}

func (b *S3Backend) url(key string) string {
	return b.endpoint + "/" + b.bucket + "/" + key
}

// Get retrieves a cached object from S3. The returned body is decompressed.
// Returns immediately if the key is not in the index (no network call).
func (b *S3Backend) Get(actionID string) (outputID string, body io.ReadCloser, size int64, t time.Time, miss bool, err error) {
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

	resp, err := b.client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cacheprog: s3 get %s: %v\n", actionID[:8], err)
		return "", nil, 0, time.Time{}, true, nil
	}

	if resp.StatusCode == 404 {
		resp.Body.Close()
		return "", nil, 0, time.Time{}, true, nil
	}
	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		fmt.Fprintf(os.Stderr, "cacheprog: s3 get %s: HTTP %d: %s\n", actionID[:8], resp.StatusCode, respBody)
		return "", nil, 0, time.Time{}, true, nil
	}

	outputID = resp.Header.Get("X-Amz-Meta-Outputid")
	if outputID == "" {
		resp.Body.Close()
		fmt.Fprintf(os.Stderr, "cacheprog: s3 get %s: missing outputid metadata\n", actionID[:8])
		return "", nil, 0, time.Time{}, true, nil
	}

	compressed, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cacheprog: s3 get %s: read body: %v\n", actionID[:8], err)
		return "", nil, 0, time.Time{}, true, nil
	}

	decompressed, err := decompressData(compressed)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cacheprog: s3 get %s: decompress: %v\n", actionID[:8], err)
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

// Put stores a cached object in S3 with LZ4 compression.
// Skips upload if the key is already in the index.
func (b *S3Backend) Put(actionID, outputID string, body io.Reader, bodySize int64) error {
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
		return fmt.Errorf("s3 put read: %w", err)
	}

	compressed, err := compressData(raw)
	if err != nil {
		return fmt.Errorf("s3 put compress: %w", err)
	}

	req, err := http.NewRequest("PUT", b.url(key), bytes.NewReader(compressed))
	if err != nil {
		return fmt.Errorf("s3 put request: %w", err)
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

	resp, err := b.client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cacheprog: s3 put %s: %v\n", actionID[:8], err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "cacheprog: s3 put %s: HTTP %d: %s\n", actionID[:8], resp.StatusCode, respBody)
		return fmt.Errorf("s3 put: HTTP %d", resp.StatusCode)
	}

	uploaded = true
	b.Stats.Puts.Increment()
	return nil
}

// removeClaimed removes a key that was optimistically added to the index
// when the upload fails, so it can be retried on the next attempt.
func (b *S3Backend) removeClaimed(key string) {
	b.keysMu.Lock()
	b.keys.Remove(key)
	b.keysMu.Unlock()
}

// Close is a no-op for S3.
func (b *S3Backend) Close() error { return nil }

func (b *S3Backend) GetStats() *CacheStats { return &b.Stats }

// signRequest authenticates an HTTP request using HTTP Basic Auth.
func (b *S3Backend) signRequest(req *http.Request) {
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
