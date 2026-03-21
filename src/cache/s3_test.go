package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wow-look-at-my/testify/require"
)

func TestCompressDecompress_RoundTrip(t *testing.T) {
	data := []byte("hello world, this is test data for compression")

	compressed, err := compressData(data)
	require.NoError(t, err)
	require.NotEmpty(t, compressed)

	decompressed, err := decompressData(compressed)
	require.NoError(t, err)
	require.Equal(t, data, decompressed)
}

func TestCompressDecompress_Empty(t *testing.T) {
	compressed, err := compressData([]byte{})
	require.NoError(t, err)

	decompressed, err := decompressData(compressed)
	require.NoError(t, err)
	require.Empty(t, decompressed)
}

func TestCompressDecompress_Large(t *testing.T) {
	data := make([]byte, 100000)
	for i := range data {
		data[i] = byte(i % 256)
	}

	compressed, err := compressData(data)
	require.NoError(t, err)

	decompressed, err := decompressData(compressed)
	require.NoError(t, err)
	require.Equal(t, data, decompressed)
}

func TestNewS3Backend_EmptyBucket(t *testing.T) {
	b, err := NewS3Backend(S3Config{})
	require.NoError(t, err)
	require.Nil(t, b)
}

func TestNewS3Backend_MissingEndpoint(t *testing.T) {
	_, err := NewS3Backend(S3Config{Bucket: "test"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "endpoint is required")
}

func TestNewS3Backend_MissingCredentials(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	_, err := NewS3Backend(S3Config{Bucket: "test", Endpoint: "http://localhost"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "required")
}

func TestNewS3Backend_DefaultPrefix(t *testing.T) {
	b, err := NewS3Backend(S3Config{
		Bucket: "test", Endpoint: "http://localhost",
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)
	require.Equal(t, "go-buildcache/", b.prefix)
}

func TestNewS3Backend_CustomPrefix(t *testing.T) {
	b, err := NewS3Backend(S3Config{
		Bucket: "test", Endpoint: "http://localhost", Prefix: "custom",
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)
	require.Equal(t, "custom/", b.prefix)
}

func TestNewS3Backend_PrefixWithSlash(t *testing.T) {
	b, err := NewS3Backend(S3Config{
		Bucket: "test", Endpoint: "http://localhost", Prefix: "already/",
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)
	require.Equal(t, "already/", b.prefix)
}

func TestNewS3Backend_DefaultRegion(t *testing.T) {
	b, err := NewS3Backend(S3Config{
		Bucket: "test", Endpoint: "http://localhost",
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)
	require.Equal(t, "us-east-1", b.region)
}

func TestS3Backend_Key(t *testing.T) {
	b := &S3Backend{prefix: "my-prefix/"}
	require.Equal(t, "my-prefix/v1abcdef", b.key("abcdef"))
}

func TestS3Backend_URL(t *testing.T) {
	b := &S3Backend{endpoint: "https://s3.example.com", bucket: "mybucket"}
	require.Equal(t, "https://s3.example.com/mybucket/go-buildcache/v1abc", b.url("go-buildcache/v1abc"))
}

func TestS3Backend_Close(t *testing.T) {
	b := &S3Backend{}
	require.NoError(t, b.Close())
}

func TestS3Backend_GetStats(t *testing.T) {
	b := &S3Backend{}
	b.Stats.Hits.Store(5)
	b.Stats.Puts.Store(3)
	stats := b.GetStats()
	require.Equal(t, uint32(5), stats.Hits.Load())
	require.Equal(t, uint32(3), stats.Puts.Load())
}

func TestS3Backend_PutAndGet(t *testing.T) {
	// Fake S3 server that stores objects in memory.
	store := map[string][]byte{}
	meta := map[string]string{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "PUT":
			body, _ := io.ReadAll(r.Body)
			store[r.URL.Path] = body
			meta[r.URL.Path] = r.Header.Get("X-Amz-Meta-Outputid")
			w.WriteHeader(200)
		case "HEAD":
			if _, ok := store[r.URL.Path]; !ok {
				w.WriteHeader(404)
				return
			}
			w.WriteHeader(200)
		case "GET":
			data, ok := store[r.URL.Path]
			if !ok {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("X-Amz-Meta-Outputid", meta[r.URL.Path])
			w.WriteHeader(200)
			w.Write(data)
		}
	}))
	defer srv.Close()

	b, err := NewS3Backend(S3Config{
		Bucket:    "testbucket",
		Endpoint:  srv.URL,
		AccessKey: "testkey",
		SecretKey: "testsecret",
	})
	require.NoError(t, err)

	// Put.
	err = b.Put("aabbccdd11223344", "eeff0011aabbccdd", nopReader("hello world"), 11)
	require.NoError(t, err)
	require.Equal(t, uint32(1), b.Stats.Puts.Load())

	// Get.
	outputID, body, size, _, miss, err := b.Get("aabbccdd11223344")
	require.NoError(t, err)
	require.False(t, miss)
	require.Equal(t, "eeff0011aabbccdd", outputID)
	data, _ := io.ReadAll(body)
	require.Equal(t, "hello world", string(data))
	require.Equal(t, int64(11), size)
	require.Equal(t, uint32(1), b.Stats.Hits.Load())
}

func TestS3Backend_GetMiss(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	b, err := NewS3Backend(S3Config{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "testkey", SecretKey: "testsecret",
	})
	require.NoError(t, err)

	_, _, _, _, miss, err := b.Get("deadbeef00000000")
	require.NoError(t, err)
	require.True(t, miss)
}

func TestS3Backend_GetMissingMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return 200 but no outputid metadata.
		w.WriteHeader(200)
		w.Write([]byte("data"))
	}))
	defer srv.Close()

	b, err := NewS3Backend(S3Config{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "testkey", SecretKey: "testsecret",
	})
	require.NoError(t, err)

	_, _, _, _, miss, _ := b.Get("deadbeef00000000")
	require.True(t, miss)
}

func TestS3Backend_PutServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	b, err := NewS3Backend(S3Config{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "testkey", SecretKey: "testsecret",
	})
	require.NoError(t, err)

	err = b.Put("aabbccdd11223344", "eeff0011aabbccdd", nopReader("data"), 4)
	require.Error(t, err)
	require.Equal(t, uint32(0), b.Stats.Puts.Load())
}

func TestSignRequest_HasAuthHeader(t *testing.T) {
	b := &S3Backend{
		region:    "us-east-1",
		accessKey: "AKID",
		secretKey: "secret",
	}
	req, _ := http.NewRequest("GET", "https://s3.example.com/bucket/key", nil)
	b.signRequest(req, nil)

	auth := req.Header.Get("Authorization")
	require.Contains(t, auth, "AWS4-HMAC-SHA256")
	require.Contains(t, auth, "Credential=AKID")
	require.NotEmpty(t, req.Header.Get("X-Amz-Date"))
	require.NotEmpty(t, req.Header.Get("X-Amz-Content-Sha256"))
}

func TestDeriveSigningKey(t *testing.T) {
	// Just verify it returns non-nil deterministic output.
	k1 := deriveSigningKey("secret", "20260315", "us-east-1", "s3")
	k2 := deriveSigningKey("secret", "20260315", "us-east-1", "s3")
	require.Equal(t, k1, k2)
	require.Len(t, k1, 32) // HMAC-SHA256 output is 32 bytes
}

// --- SigV4 spec compliance tests ---
// Test vectors from https://docs.aws.amazon.com/AmazonS3/latest/API/sig-v4-header-based-auth.html

const (
	awsTestAccessKey = "AKIAIOSFODNN7EXAMPLE"
	awsTestSecretKey = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	awsTestRegion    = "us-east-1"
)

var awsTestTime = time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)

// emptyPayloadHash is SHA256(""), used in AWS test vectors for GET requests.
var emptyPayloadHash = func() string {
	h := sha256.Sum256([]byte{})
	return hex.EncodeToString(h[:])
}()

func TestSortedQueryString(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"single", "prefix=foo", "prefix=foo"},
		{"already sorted", "max-keys=2&prefix=J", "max-keys=2&prefix=J"},
		{"unsorted - the bug case", "list-type=2&prefix=x&max-keys=1000", "list-type=2&max-keys=1000&prefix=x"},
		{"with continuation-token", "list-type=2&prefix=x&max-keys=1000&continuation-token=abc",
			"continuation-token=abc&list-type=2&max-keys=1000&prefix=x"},
		{"value-less param", "lifecycle", "lifecycle="},
		{"encoded slash round-trips", "list-type=2&prefix=go-buildcache%2F&max-keys=1000",
			"list-type=2&max-keys=1000&prefix=go-buildcache%2F"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, sortedQueryString(tt.in))
		})
	}
}

func TestDeriveSigningKey_AWSVector(t *testing.T) {
	// Use the AWS example secret key and date to derive the signing key,
	// then verify it produces the correct signature for Example 4 (List Objects).
	signingKey := deriveSigningKey(awsTestSecretKey, "20130524", awsTestRegion, "s3")
	require.Len(t, signingKey, 32)

	// Example 4 string to sign (Get Bucket / List Objects with ?max-keys=2&prefix=J)
	stringToSign := "AWS4-HMAC-SHA256\n" +
		"20130524T000000Z\n" +
		"20130524/us-east-1/s3/aws4_request\n" +
		"df57d21db20da04d7fa30298dd4488ba3a2b47ca3a489c74750e0f1e7df1b9b7"

	sig := hmacSHA256(signingKey, []byte(stringToSign))
	signature := hex.EncodeToString(sig)
	require.Equal(t, "34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7", signature)
}

func TestBuildCanonicalHeaders_Sorted(t *testing.T) {
	b := &S3Backend{}
	req, _ := http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com/", nil)
	// Set headers in non-alphabetical order.
	req.Header.Set("X-Amz-Date", "20130524T000000Z")
	req.Header.Set("X-Amz-Content-Sha256", emptyPayloadHash)
	req.Header.Set("Host", "examplebucket.s3.amazonaws.com")

	signedHeaders, canonicalHeaders := b.buildCanonicalHeaders(req)

	// Must be alphabetically sorted.
	require.Equal(t, "host;x-amz-content-sha256;x-amz-date", signedHeaders)
	// Each header on its own line, with trailing newline.
	lines := strings.Split(canonicalHeaders, "\n")
	require.Equal(t, "host:examplebucket.s3.amazonaws.com", lines[0])
	require.Equal(t, "x-amz-content-sha256:"+emptyPayloadHash, lines[1])
	require.Equal(t, "x-amz-date:20130524T000000Z", lines[2])
}

func TestSignRequest_AWSExample4_ListObjects(t *testing.T) {
	// AWS Example 4: GET Bucket (List Objects) with query params.
	// Request: GET /?max-keys=2&prefix=J
	// Host: examplebucket.s3.amazonaws.com
	//
	// This is the exact scenario that was broken (query params in signing).
	// Note: our implementation only signs host + x-amz-* headers, and uses
	// UNSIGNED-PAYLOAD for nil payload. The AWS example signs with the actual
	// empty payload hash. To match the AWS vector exactly, we pre-set the
	// x-amz-content-sha256 header to the real empty hash before signing.
	b := &S3Backend{
		region:    awsTestRegion,
		accessKey: awsTestAccessKey,
		secretKey: awsTestSecretKey,
	}

	req, _ := http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com/?max-keys=2&prefix=J", nil)
	// Pre-set content hash to match AWS test vector (they use real empty hash, not UNSIGNED-PAYLOAD).
	req.Header.Set("X-Amz-Content-Sha256", emptyPayloadHash)

	// Sign with fixed timestamp matching the AWS test vector.
	b.signRequestAt(req, nil, awsTestTime)

	// The signRequestAt will overwrite X-Amz-Content-Sha256 with UNSIGNED-PAYLOAD
	// since payload is nil. To test against the AWS vector, we need to pass the
	// empty body as payload. Let's re-do with empty payload bytes.
	req, _ = http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com/?max-keys=2&prefix=J", nil)
	b.signRequestAt(req, []byte{}, awsTestTime)

	auth := req.Header.Get("Authorization")
	require.Contains(t, auth, "AWS4-HMAC-SHA256")
	require.Contains(t, auth, "Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request")
	require.Contains(t, auth, "SignedHeaders=host;x-amz-content-sha256;x-amz-date")
	require.Contains(t, auth, "Signature=34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7")
}

func TestSignRequest_AWSExample3_GetBucketLifecycle(t *testing.T) {
	// AWS Example 3: GET /?lifecycle
	// Expected signature: fea454ca298b7da1c68078a5d1bdbfbbe0d65c699e0f91ac7a200a0136783543
	b := &S3Backend{
		region:    awsTestRegion,
		accessKey: awsTestAccessKey,
		secretKey: awsTestSecretKey,
	}

	req, _ := http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com/?lifecycle", nil)
	b.signRequestAt(req, []byte{}, awsTestTime)

	auth := req.Header.Get("Authorization")
	require.Contains(t, auth, "Signature=fea454ca298b7da1c68078a5d1bdbfbbe0d65c699e0f91ac7a200a0136783543")
}

func TestSignRequest_WithQueryParams_ChangesSignature(t *testing.T) {
	// Verify that different query params produce different signatures.
	b := &S3Backend{
		region:    "us-east-1",
		accessKey: "AKID",
		secretKey: "secret",
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	req1, _ := http.NewRequest("GET", "https://s3.example.com/bucket?prefix=a", nil)
	b.signRequestAt(req1, nil, now)
	sig1 := req1.Header.Get("Authorization")

	req2, _ := http.NewRequest("GET", "https://s3.example.com/bucket?prefix=b", nil)
	b.signRequestAt(req2, nil, now)
	sig2 := req2.Header.Get("Authorization")

	require.NotEqual(t, sig1, sig2, "different query params must produce different signatures")
}

func TestSignRequest_UnsortedQueryParams_MatchesSorted(t *testing.T) {
	// Verify that query param order doesn't affect the signature.
	b := &S3Backend{
		region:    "us-east-1",
		accessKey: "AKID",
		secretKey: "secret",
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	req1, _ := http.NewRequest("GET", "https://s3.example.com/bucket?prefix=x&max-keys=10", nil)
	b.signRequestAt(req1, nil, now)
	sig1 := req1.Header.Get("Authorization")

	req2, _ := http.NewRequest("GET", "https://s3.example.com/bucket?max-keys=10&prefix=x", nil)
	b.signRequestAt(req2, nil, now)
	sig2 := req2.Header.Get("Authorization")

	require.Equal(t, sig1, sig2, "parameter order must not affect signature")
}

func nopReader(s string) io.Reader {
	return strings.NewReader(s)
}
