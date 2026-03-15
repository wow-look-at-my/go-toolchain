package cache

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

func nopReader(s string) io.Reader {
	return strings.NewReader(s)
}
