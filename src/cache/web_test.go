package cache

import (
	"encoding/base64"
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

func TestNewWebBackend_EmptyBucket(t *testing.T) {
	b, err := NewWebBackend(WebConfig{})
	require.NoError(t, err)
	require.Nil(t, b)
}

func TestNewWebBackend_MissingEndpoint(t *testing.T) {
	_, err := NewWebBackend(WebConfig{Bucket: "test"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "endpoint is required")
}

func TestNewWebBackend_MissingCredentials(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	_, err := NewWebBackend(WebConfig{Bucket: "test", Endpoint: "http://localhost"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "required")
}

func TestNewWebBackend_DefaultPrefix(t *testing.T) {
	b, err := NewWebBackend(WebConfig{
		Bucket: "test", Endpoint: "http://localhost",
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)
	require.Equal(t, "go-buildcache/", b.prefix)
}

func TestNewWebBackend_CustomPrefix(t *testing.T) {
	b, err := NewWebBackend(WebConfig{
		Bucket: "test", Endpoint: "http://localhost", Prefix: "custom",
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)
	require.Equal(t, "custom/", b.prefix)
}

func TestNewWebBackend_PrefixWithSlash(t *testing.T) {
	b, err := NewWebBackend(WebConfig{
		Bucket: "test", Endpoint: "http://localhost", Prefix: "already/",
		AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)
	require.Equal(t, "already/", b.prefix)
}

func TestWebBackend_Key(t *testing.T) {
	b := &WebBackend{prefix: "my-prefix/"}
	require.Equal(t, "my-prefix/v1abcdef", b.key("abcdef"))
}

func TestWebBackend_URL(t *testing.T) {
	b := &WebBackend{endpoint: "https://s3.example.com", bucket: "mybucket"}
	require.Equal(t, "https://s3.example.com/mybucket/go-buildcache/v1abc", b.url("go-buildcache/v1abc"))
}

func TestWebBackend_Close(t *testing.T) {
	b := &WebBackend{}
	require.NoError(t, b.Close())
}

func TestWebBackend_GetStats(t *testing.T) {
	b := &WebBackend{}
	b.Stats.Hits.Store(5)
	b.Stats.Puts.Store(3)
	stats := b.GetStats()
	require.Equal(t, uint32(5), stats.Hits.Load())
	require.Equal(t, uint32(3), stats.Puts.Load())
}

func TestWebBackend_PutAndGet(t *testing.T) {
	// Fake server that stores objects in memory.
	store := map[string][]byte{}
	headers := map[string]http.Header{} // capture all headers per path

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "PUT":
			body, _ := io.ReadAll(r.Body)
			store[r.URL.Path] = body
			headers[r.URL.Path] = r.Header.Clone()
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
			h := headers[r.URL.Path]
			w.Header().Set("X-Amz-Meta-Outputid", h.Get("X-Amz-Meta-Outputid"))
			w.WriteHeader(200)
			w.Write(data)
		}
	}))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket:    "testbucket",
		Endpoint:  srv.URL,
		AccessKey: "testkey",
		SecretKey: "testsecret",
		Version:   "v1.2.3",
	})
	require.NoError(t, err)

	// Put.
	err = b.Put("aabbccdd11223344", "eeff0011aabbccdd", nopReader("hello world"), 11)
	require.NoError(t, err)
	require.Equal(t, uint32(1), b.Stats.Puts.Load())

	// Verify metadata headers were sent.
	h := headers["/testbucket/go-buildcache/v1aabbccdd11223344"]
	require.Equal(t, "eeff0011aabbccdd", h.Get("X-Amz-Meta-Outputid"))
	require.Equal(t, "unknown", h.Get("X-Amz-Meta-Object-Type"))
	require.Equal(t, "11", h.Get("X-Amz-Meta-Body-Size"))
	require.Equal(t, "lz4", h.Get("X-Amz-Meta-Compression"))
	require.NotEmpty(t, h.Get("X-Amz-Meta-Created"))
	require.Equal(t, "v1.2.3", h.Get("X-Amz-Meta-Toolchain-Version"))
	// Plain text body has no go object header, so these should be absent.
	require.Empty(t, h.Get("X-Amz-Meta-Go-Version"))
	require.Empty(t, h.Get("X-Amz-Meta-Target"))

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

func TestWebBackend_PutArchiveMetadata(t *testing.T) {
	// Verify that Go archive bodies get Go-Version and Target metadata.
	headers := map[string]http.Header{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			io.ReadAll(r.Body)
			headers[r.URL.Path] = r.Header.Clone()
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "testkey", SecretKey: "testsecret",
	})
	require.NoError(t, err)

	// Simulate a Go archive body with __.PKGDEF containing a go object header.
	archiveBody := "!<arch>\n__.PKGDEF       0           0     0     644     100       `\ngo object linux amd64 go1.24.7 X:regabiwrappers\nsome export data here\n"
	err = b.Put("1111111122222222", "3333333344444444", nopReader(archiveBody), int64(len(archiveBody)))
	require.NoError(t, err)

	h := headers["/testbucket/go-buildcache/v11111111122222222"]
	require.Equal(t, "go-archive", h.Get("X-Amz-Meta-Object-Type"))
	require.Equal(t, "go1.24.7", h.Get("X-Amz-Meta-Go-Version"))
	require.Equal(t, "linux/amd64", h.Get("X-Amz-Meta-Target"))
}

func TestWebBackend_PutNoVersionWhenEmpty(t *testing.T) {
	headers := map[string]http.Header{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			io.ReadAll(r.Body)
			headers[r.URL.Path] = r.Header.Clone()
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "testkey", SecretKey: "testsecret",
		// Version intentionally empty.
	})
	require.NoError(t, err)

	err = b.Put("aaaa000011112222", "bbbb333344445555", nopReader("x"), 1)
	require.NoError(t, err)

	h := headers["/testbucket/go-buildcache/v1aaaa000011112222"]
	require.Empty(t, h.Get("X-Amz-Meta-Toolchain-Version"))
}

func TestWebBackend_GetMiss(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "testkey", SecretKey: "testsecret",
	})
	require.NoError(t, err)

	_, _, _, _, miss, err := b.Get("deadbeef00000000")
	require.NoError(t, err)
	require.True(t, miss)
}

func TestWebBackend_GetMissingMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return 200 but no outputid metadata.
		w.WriteHeader(200)
		w.Write([]byte("data"))
	}))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "testkey", SecretKey: "testsecret",
	})
	require.NoError(t, err)

	_, _, _, _, miss, _ := b.Get("deadbeef00000000")
	require.True(t, miss)
}

func TestWebBackend_PutServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "testkey", SecretKey: "testsecret",
	})
	require.NoError(t, err)

	err = b.Put("aabbccdd11223344", "eeff0011aabbccdd", nopReader("data"), 4)
	require.Error(t, err)
	require.Equal(t, uint32(0), b.Stats.Puts.Load())
}

func TestSignRequest_BasicAuth(t *testing.T) {
	b := &WebBackend{
		accessKey: "AKID",
		secretKey: "secret",
	}
	req, _ := http.NewRequest("GET", "https://s3.example.com/bucket/key", nil)
	b.signRequest(req)

	auth := req.Header.Get("Authorization")
	require.True(t, strings.HasPrefix(auth, "Basic "))

	decoded, err := base64.StdEncoding.DecodeString(auth[len("Basic "):])
	require.NoError(t, err)
	require.Equal(t, "AKID:secret", string(decoded))
}

func TestNewWebBackend_EndpointSchemeNormalization(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     string
	}{
		{"bare host", "s3.example.com", "https://s3.example.com"},
		{"with https", "https://s3.example.com", "https://s3.example.com"},
		{"with http", "http://localhost:9000", "http://localhost:9000"},
		{"trailing slash", "s3.example.com/", "https://s3.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := NewWebBackend(WebConfig{
				Bucket: "test", Endpoint: tt.endpoint,
				AccessKey: "key", SecretKey: "secret",
			})
			require.NoError(t, err)
			require.Equal(t, tt.want, b.endpoint)
		})
	}
}

func TestDetectObjectType(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"go archive", []byte("!<arch>\nrest of archive"), "go-archive"},
		{"elf binary", []byte{0x7f, 'E', 'L', 'F', 2, 1, 0, 0}, "elf-binary"},
		{"macho 64-bit LE", []byte{0xcf, 0xfa, 0xed, 0xfe, 0, 0, 0, 0}, "macho-binary"},
		{"macho 64-bit BE", []byte{0xfe, 0xed, 0xfa, 0xcf, 0, 0, 0, 0}, "macho-binary"},
		{"macho 32-bit", []byte{0xfe, 0xed, 0xfa, 0xce, 0, 0, 0, 0}, "macho-binary"},
		{"macho universal", []byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 0}, "macho-binary"},
		{"pe binary", []byte{'M', 'Z', 0, 0, 0, 0, 0, 0}, "pe-binary"},
		{"go object", []byte{0x00, 'g', 'o', '1', '2', '0', 'l', 'd'}, "go-object"},
		{"random data", []byte{0x01, 0x02, 0x03, 0x04}, "unknown"},
		{"empty", []byte{}, "unknown"},
		{"too short for archive", []byte("!<arch"), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, detectObjectType(tt.data))
		})
	}
}

func TestParseArchiveHeader(t *testing.T) {
	tests := []struct {
		name       string
		data       []byte
		wantGoVer  string
		wantTarget string
	}{
		{
			"valid archive with go object line",
			[]byte("!<arch>\n__.PKGDEF       0           0     0     644     100       `\ngo object linux amd64 go1.24.7 X:regabiwrappers\nexport data\n"),
			"go1.24.7", "linux/amd64",
		},
		{
			"archive with darwin arm64",
			[]byte("!<arch>\n__.PKGDEF       0           0     0     644     50        `\ngo object darwin arm64 go1.25.0\nmore data\n"),
			"go1.25.0", "darwin/arm64",
		},
		{
			"archive without go object line",
			[]byte("!<arch>\n__.PKGDEF       0           0     0     644     50        `\nsome other content\n"),
			"", "",
		},
		{
			"non-archive data",
			[]byte("hello world this is not an archive"),
			"", "",
		},
		{
			"empty data",
			[]byte{},
			"", "",
		},
		{
			"go object not at line start",
			[]byte("prefix go object linux amd64 go1.24.7\n"),
			"", "",
		},
		{
			"go object line too short",
			[]byte("go object linux amd64\n"),
			"", "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			goVer, target := parseArchiveHeader(tt.data)
			require.Equal(t, tt.wantGoVer, goVer)
			require.Equal(t, tt.wantTarget, target)
		})
	}
}

func nopReader(s string) io.Reader {
	return strings.NewReader(s)
}
