package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
			w.Header().Set("X-Cache-Meta-Outputid", h.Get("X-Cache-Meta-Outputid"))
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
	// Exercise the synchronous single-PUT path (the batch-unsupported fallback),
	// which sends one HTTP PUT per object with the X-Cache-Meta-* headers this
	// test asserts. (The coalesced batch path is covered in batchput_test.go.)
	b.batchPutUnsupported.Store(true)

	// Use a payload >= batchSizeThreshold so it's uploaded individually.
	payload := largePayload(1024)
	outputID := testOutputID(payload)

	// Put.
	err = b.Put("aabbccdd11223344", outputID, nopReader(payload), int64(len(payload)))
	require.NoError(t, err)
	require.Equal(t, uint32(1), b.Stats.Puts.Load())

	// Verify metadata headers were sent.
	h := headers["/testbucket/go-buildcache/v1aabbccdd11223344"]
	require.Equal(t, outputID, h.Get("X-Cache-Meta-Outputid"))
	require.Equal(t, "unknown", h.Get("X-Cache-Meta-Object-Type"))
	require.Equal(t, strconv.Itoa(len(payload)), h.Get("X-Cache-Meta-Body-Size"))
	require.Equal(t, "lz4", h.Get("X-Cache-Meta-Compression"))
	require.NotEmpty(t, h.Get("X-Cache-Meta-Created"))
	require.Equal(t, "v1.2.3", h.Get("X-Cache-Meta-Toolchain-Version"))
	// Plain text body has no go object header, so these should be absent.
	require.Empty(t, h.Get("X-Cache-Meta-Go-Version"))
	require.Empty(t, h.Get("X-Cache-Meta-Target"))

	// Get.
	gotOutputID, body, size, _, miss, err := b.Get("aabbccdd11223344")
	require.NoError(t, err)
	require.False(t, miss)
	require.Equal(t, outputID, gotOutputID)
	data, _ := io.ReadAll(body)
	require.Equal(t, payload, string(data))
	require.Equal(t, int64(len(payload)), size)
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
	b.batchPutUnsupported.Store(true) // assert single-PUT headers synchronously

	// Simulate a Go archive body with __.PKGDEF containing a go object header.
	// Pad to >= batchSizeThreshold so it's uploaded individually.
	archiveBody := "!<arch>\n__.PKGDEF       0           0     0     644     100       `\ngo object linux amd64 go1.24.7 X:regabiwrappers\nsome export data here\n"
	archiveBody += largePayload(1024)
	err = b.Put("1111111122222222", "3333333344444444", nopReader(archiveBody), int64(len(archiveBody)))
	require.NoError(t, err)

	h := headers["/testbucket/go-buildcache/v11111111122222222"]
	require.Equal(t, "go-archive", h.Get("X-Cache-Meta-Object-Type"))
	require.Equal(t, "go1.24.7", h.Get("X-Cache-Meta-Go-Version"))
	require.Equal(t, "linux/amd64", h.Get("X-Cache-Meta-Target"))
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
	b.batchPutUnsupported.Store(true) // assert single-PUT headers synchronously

	payload := largePayload(1024)
	err = b.Put("aaaa000011112222", "bbbb333344445555", nopReader(payload), int64(len(payload)))
	require.NoError(t, err)

	h := headers["/testbucket/go-buildcache/v1aaaa000011112222"]
	require.Empty(t, h.Get("X-Cache-Meta-Toolchain-Version"))
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

// primeIndex forces a key into the in-memory index so subsequent Gets
// take the getIndividual path instead of falling through to batch GET.
// Test helper only — in production, keys enter the index via the GBCI
// blob loaded by loadOrFetchIndex, or the check-and-claim step in Put.
func primeIndex(b *WebBackend, actionID string) {
	b.keysMu.Lock()
	b.keys.Add(b.key(actionID))
	b.keysMu.Unlock()
}

// TestWebBackend_GetIndividualMissPaths drives each error branch in
// getIndividual by routing Gets past the batch fallback. The branches
// are the same ones that emit miss-reason spans, so this also guards
// against span-tagging regressions.
func TestWebBackend_GetIndividualMissPaths(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"404", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }},
		{"http_error", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(500)
			w.Write([]byte("boom"))
		}},
		{"no_outputid", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			w.Write([]byte("data-without-outputid-header"))
		}},
		{"decompress", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Cache-Meta-Outputid", "aabbccdd")
			w.WriteHeader(200)
			// Not LZ4 — will fail decompress.
			w.Write([]byte("not lz4 framed data"))
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()

			b, err := NewWebBackend(WebConfig{
				Bucket: "testbucket", Endpoint: srv.URL,
				AccessKey: "testkey", SecretKey: "testsecret",
			})
			require.NoError(t, err)
			primeIndex(b, "deadbeef00000000")

			_, _, _, _, miss, _ := b.Get("deadbeef00000000")
			require.True(t, miss, "expected miss for %s path", tc.name)
		})
	}
}

// TestWebBackend_GetRejectsCorruptBody is the regression test for the "corrupt
// index" failure. A remote object whose body does not hash to its advertised
// outputID is corrupt (truncated, badly decoded, or poisoned/rotted remotely) and
// must be refused — treated as a miss, never served — so the go command never
// consumes a damaged object. The key is also evicted from the in-memory index
// so a later recompute re-uploads (overwrites) it clean instead of skipping the
// Put as already-present.
func TestWebBackend_GetRejectsCorruptBody(t *testing.T) {
	const actionID = "aabbccdd11223344"
	// outputID advertises the hash of the CORRECT body, but the server serves a
	// different (corrupt) body of the same length under it.
	good := largePayload(2048)
	corrupt := good[:len(good)-10] + "XXXXXXXXXX"
	outputID := testOutputID(good)
	require.NotEqual(t, outputID, testOutputID(corrupt))

	objectPath := "/testbucket/go-buildcache/v1" + actionID
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != objectPath {
			w.WriteHeader(404) // index fetch etc.
			return
		}
		w.Header().Set("X-Cache-Meta-Outputid", outputID)
		w.WriteHeader(200)
		c, _ := compressData([]byte(corrupt))
		w.Write(c)
	}))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "testkey", SecretKey: "testsecret",
	})
	require.NoError(t, err)
	defer b.Close()
	primeIndex(b, actionID)

	contains := func() bool {
		b.keysMu.RLock()
		defer b.keysMu.RUnlock()
		return b.keys.Contains(b.key(actionID))
	}
	require.True(t, contains(), "precondition: key is in the index")

	_, _, _, _, miss, err := b.Get(actionID)
	require.NoError(t, err)
	require.True(t, miss, "a corrupt body must be treated as a miss, never served")
	require.Equal(t, uint32(1), b.MissChecksum.Load())
	require.Equal(t, uint32(1), b.Stats.Corrupt.Load())
	require.Equal(t, uint32(0), b.Stats.Hits.Load())
	require.False(t, contains(), "corrupt key must be evicted from the index so a recompute re-uploads it clean")
}

// TestWebBackend_GetServesCorrectBody is the positive control for the integrity
// check: a body that hashes to its advertised outputID is served as a hit.
func TestWebBackend_GetServesCorrectBody(t *testing.T) {
	const actionID = "aabbccdd11223344"
	good := largePayload(2048)
	outputID := testOutputID(good)

	objectPath := "/testbucket/go-buildcache/v1" + actionID
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != objectPath {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("X-Cache-Meta-Outputid", outputID)
		w.WriteHeader(200)
		c, _ := compressData([]byte(good))
		w.Write(c)
	}))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "testkey", SecretKey: "testsecret",
	})
	require.NoError(t, err)
	defer b.Close()
	primeIndex(b, actionID)

	gotOutputID, body, size, _, miss, err := b.Get(actionID)
	require.NoError(t, err)
	require.False(t, miss)
	require.Equal(t, outputID, gotOutputID)
	require.Equal(t, uint32(0), b.MissChecksum.Load())
	require.Equal(t, uint32(1), b.Stats.Hits.Load())
	data, _ := io.ReadAll(body)
	require.Equal(t, good, string(data))
	require.Equal(t, int64(len(good)), size)
}

// TestWebBackend_GetLegacyAmzMetaFallback verifies the deprecated read path: a
// new client still reads the outputid from an older cache server that emits only
// the legacy S3-style X-Amz-Meta-Outputid header (no native X-Cache-Meta-*).
func TestWebBackend_GetLegacyAmzMetaFallback(t *testing.T) {
	const actionID = "aabbccdd11223344"
	good := largePayload(2048)
	outputID := testOutputID(good)

	objectPath := "/testbucket/go-buildcache/v1" + actionID
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != objectPath {
			w.WriteHeader(404)
			return
		}
		// Only the deprecated header — simulates a not-yet-upgraded server.
		w.Header().Set("X-Amz-Meta-Outputid", outputID)
		w.WriteHeader(200)
		c, _ := compressData([]byte(good))
		w.Write(c)
	}))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "testkey", SecretKey: "testsecret",
	})
	require.NoError(t, err)
	defer b.Close()
	primeIndex(b, actionID)

	gotOutputID, body, _, _, miss, err := b.Get(actionID)
	require.NoError(t, err)
	require.False(t, miss, "client must fall back to X-Amz-Meta-Outputid when the native header is absent")
	require.Equal(t, outputID, gotOutputID)
	require.Equal(t, uint32(1), b.Stats.Hits.Load())
	data, _ := io.ReadAll(body)
	require.Equal(t, good, string(data))
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
	// Force the synchronous single-PUT path so the HTTP-error result (and its
	// errLogged wrapping) is returned from Put directly, as this test asserts.
	b.batchPutUnsupported.Store(true)

	payload := largePayload(1024)
	err = b.Put("aabbccdd11223344", "eeff0011aabbccdd", nopReader(payload), int64(len(payload)))
	require.Error(t, err)
	require.True(t, errors.Is(err, errLogged), "PUT HTTP error must wrap errLogged so cache.go suppresses the duplicate log")
	require.Equal(t, uint32(0), b.Stats.Puts.Load())
}

func TestWebBackend_PutServerError_Coalesced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		w.Write([]byte("error code: 502"))
	}))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "testkey", SecretKey: "testsecret",
	})
	require.NoError(t, err)
	// This test exercises per-object PUT error coalescing in the error logger,
	// so drive the synchronous single-PUT path (one "web put" record per object).
	b.batchPutUnsupported.Store(true)

	// Swap the auto-initialized 30s logger for one bound to a buffer with
	// a long interval, so all flushing happens on Close.
	_ = b.errLog.Close()
	var buf bytes.Buffer
	b.errLog = newHTTPErrLogger(&buf, time.Hour, b.tracer)

	const n = 10
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			actionID := strings.Repeat("0", 14) + strconv.FormatInt(int64(i), 16) + "0"
			outputID := "eeff0011aabbccdd"
			payload := largePayload(64)
			_ = b.Put(actionID, outputID, nopReader(payload), int64(len(payload)))
		}(i)
	}
	wg.Wait()
	require.NoError(t, b.Close())

	out := buf.String()
	require.Equal(t, 1, strings.Count(out, "\n"), "expected exactly one aggregated line, got: %q", out)
	require.Contains(t, out, "cacheprog: web put ")
	require.Contains(t, out, "HTTP 502: error code: 502")
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

func TestWebBackend_PutPreservesMethodOnRedirect(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{"301", http.StatusMovedPermanently},
		{"302", http.StatusFound},
		{"307", http.StatusTemporaryRedirect},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotMethod string
			var gotBody []byte
			var gotAuth string

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/testbucket/go-buildcache/v1aabbccdd11223344" {
					// First request: redirect to /final.
					http.Redirect(w, r, "/final", tt.status)
					return
				}
				if r.URL.Path == "/final" {
					gotMethod = r.Method
					gotBody, _ = io.ReadAll(r.Body)
					gotAuth = r.Header.Get("Authorization")
					w.WriteHeader(200)
					return
				}
				w.WriteHeader(404)
			}))
			defer srv.Close()

			b, err := NewWebBackend(WebConfig{
				Bucket: "testbucket", Endpoint: srv.URL,
				AccessKey: "testkey", SecretKey: "testsecret",
			})
			require.NoError(t, err)
			b.batchPutUnsupported.Store(true) // single PUT to the object path under test

			payload := largePayload(1024)
			err = b.Put("aabbccdd11223344", "eeff0011aabbccdd", nopReader(payload), int64(len(payload)))
			require.NoError(t, err)
			require.Equal(t, "PUT", gotMethod, "redirect should preserve PUT method")
			require.NotEmpty(t, gotBody, "redirect should preserve request body")
			require.NotEmpty(t, gotAuth, "redirect should preserve Authorization header")
			require.Equal(t, uint32(1), b.Stats.Puts.Load())
		})
	}
}

func nopReader(s string) io.Reader {
	return strings.NewReader(s)
}

// testOutputID returns the cache outputID for a body: its lowercase-hex
// SHA-256, exactly as the go command derives it. Web-tier GETs verify the
// served body against this id (see outputIDMatches), so any test that exercises
// a cache hit must advertise the body's real hash, not an arbitrary string.
func testOutputID(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

// largePayload returns a payload of exactly n bytes (>= batchSizeThreshold)
// so that Put uploads it individually rather than batching it.
func largePayload(n int) string {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = byte('A' + i%26)
	}
	return string(buf)
}
