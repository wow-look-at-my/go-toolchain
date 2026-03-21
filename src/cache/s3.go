package cache

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pierrec/lz4/v4"
	"github.com/wow-look-at-my/go-containers/set"
)

// S3Config holds the configuration for an S3-compatible backend.
type S3Config struct {
	Bucket    string // Required. Empty bucket disables the backend.
	Region    string // AWS region for SigV4 signing. Defaults to "us-east-1".
	Endpoint  string // S3 endpoint URL (e.g. "https://s3.pazer.io"). Required.
	Prefix    string // Key prefix (defaults to "go-buildcache/").
	AccessKey string // AWS_ACCESS_KEY_ID
	SecretKey string // AWS_SECRET_ACCESS_KEY
}

// S3Backend stores cache objects in an S3-compatible bucket with LZ4 compression.
type S3Backend struct {
	client    *http.Client
	bucket    string
	prefix    string
	region    string
	endpoint  string
	accessKey string
	secretKey string
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
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	accessKey := cfg.AccessKey
	if accessKey == "" {
		accessKey = os.Getenv("AWS_ACCESS_KEY_ID")
	}
	secretKey := cfg.SecretKey
	if secretKey == "" {
		secretKey = os.Getenv("AWS_SECRET_ACCESS_KEY")
	}
	if accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("s3: AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY are required")
	}

	b := &S3Backend{
		client:    &http.Client{Timeout: 30 * time.Second},
		bucket:    cfg.Bucket,
		prefix:    prefix,
		region:    region,
		endpoint:  strings.TrimRight(cfg.Endpoint, "/"),
		accessKey: accessKey,
		secretKey: secretKey,
	}
	b.keys = b.listAllKeys()
	fmt.Fprintf(os.Stderr, "cacheprog: s3 index: %d keys\n", b.keys.Len())
	return b, nil
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
		b.signRequest(req, nil)
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
	b.signRequest(req, nil)

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
	b.signRequest(req, compressed)

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

// signRequest signs an HTTP request using AWS SigV4.
func (b *S3Backend) signRequest(req *http.Request, payload []byte) {
	b.signRequestAt(req, payload, time.Now().UTC())
}

// signRequestAt signs an HTTP request using AWS SigV4 with an explicit timestamp.
// Extracted for testability against AWS official test vectors.
func (b *S3Backend) signRequestAt(req *http.Request, payload []byte, now time.Time) {
	datestamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")

	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("Host", req.URL.Host)

	// Payload hash.
	var payloadHash string
	if payload != nil {
		h := sha256.Sum256(payload)
		payloadHash = hex.EncodeToString(h[:])
	} else {
		payloadHash = "UNSIGNED-PAYLOAD"
	}
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	// Canonical request.
	signedHeaders, canonicalHeaders := b.buildCanonicalHeaders(req)
	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.Path,
		sortedQueryString(req.URL.RawQuery),
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	// String to sign.
	scope := datestamp + "/" + b.region + "/s3/aws4_request"
	canonHash := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(canonHash[:])

	// Signing key.
	signingKey := deriveSigningKey(b.secretKey, datestamp, b.region, "s3")

	// Signature.
	sig := hmacSHA256(signingKey, []byte(stringToSign))
	signature := hex.EncodeToString(sig)

	// Authorization header.
	auth := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		b.accessKey, scope, signedHeaders, signature)
	req.Header.Set("Authorization", auth)
}

func (b *S3Backend) buildCanonicalHeaders(req *http.Request) (signedHeaders, canonicalHeaders string) {
	// Only sign host, x-amz-* headers. Keep it minimal for S3-compatible services.
	type hdr struct{ k, v string }
	var hdrs []hdr
	hdrs = append(hdrs, hdr{"host", req.URL.Host})
	for k, vals := range req.Header {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "x-amz-") {
			hdrs = append(hdrs, hdr{lk, strings.TrimSpace(vals[0])})
		}
	}
	// Sort by key.
	for i := 0; i < len(hdrs); i++ {
		for j := i + 1; j < len(hdrs); j++ {
			if hdrs[j].k < hdrs[i].k {
				hdrs[i], hdrs[j] = hdrs[j], hdrs[i]
			}
		}
	}
	var names, canonical []string
	for _, h := range hdrs {
		names = append(names, h.k)
		canonical = append(canonical, h.k+":"+h.v)
	}
	signedHeaders = strings.Join(names, ";")
	canonicalHeaders = strings.Join(canonical, "\n") + "\n"
	return
}

func deriveSigningKey(secret, datestamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(datestamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// sortedQueryString returns the canonical query string: parameters sorted by
// key, each key followed by "=" and its value (even if empty). This matches
// the AWS SigV4 spec for canonical query strings.
func sortedQueryString(raw string) string {
	if raw == "" {
		return ""
	}
	vals, err := url.ParseQuery(raw)
	if err != nil {
		// Shouldn't happen; fall back to raw.
		return raw
	}
	// url.Values.Encode sorts by key and always includes "=".
	return vals.Encode()
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
