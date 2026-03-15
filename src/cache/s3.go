package cache

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pierrec/lz4/v4"
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
	Stats     CacheStats
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

	return &S3Backend{
		client:    &http.Client{Timeout: 30 * time.Second},
		bucket:    cfg.Bucket,
		prefix:    prefix,
		region:    region,
		endpoint:  strings.TrimRight(cfg.Endpoint, "/"),
		accessKey: accessKey,
		secretKey: secretKey,
	}, nil
}

func (b *S3Backend) key(actionID string) string {
	return b.prefix + "v1" + actionID
}

func (b *S3Backend) url(key string) string {
	return b.endpoint + "/" + b.bucket + "/" + key
}

// Get retrieves a cached object from S3. The returned body is decompressed.
func (b *S3Backend) Get(actionID string) (outputID string, body io.ReadCloser, size int64, t time.Time, miss bool, err error) {
	key := b.key(actionID)
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

// Exists checks if an object exists in S3 via HEAD request.
func (b *S3Backend) Exists(actionID string) bool {
	key := b.key(actionID)
	req, err := http.NewRequest("HEAD", b.url(key), nil)
	if err != nil {
		return false
	}
	b.signRequest(req, nil)
	resp, err := b.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// Put stores a cached object in S3 with LZ4 compression.
// Skips upload if the object already exists.
func (b *S3Backend) Put(actionID, outputID string, body io.Reader, bodySize int64) error {
	if b.Exists(actionID) {
		return nil
	}

	raw, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("s3 put read: %w", err)
	}

	compressed, err := compressData(raw)
	if err != nil {
		return fmt.Errorf("s3 put compress: %w", err)
	}

	key := b.key(actionID)
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

	b.Stats.Puts.Increment()
	return nil
}

// Close is a no-op for S3.
func (b *S3Backend) Close() error { return nil }

func (b *S3Backend) GetStats() *CacheStats { return &b.Stats }

// signRequest signs an HTTP request using AWS SigV4.
func (b *S3Backend) signRequest(req *http.Request, payload []byte) {
	now := time.Now().UTC()
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
		req.URL.RawQuery,
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
