package cache

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsGoModuleIndex(t *testing.T) {
	// Real index blobs: the version line is the first thing modindex writes.
	require.True(t, isGoModuleIndex([]byte("go index v2\n\x00\x00stuff")))
	require.True(t, isGoModuleIndex([]byte("go index v1\nlegacy")))
	require.True(t, isGoModuleIndex([]byte("go index v999\nfuture format")))

	// Not an index: a compiled package archive, plain output, near-misses, empty.
	require.False(t, isGoModuleIndex(archiveWithBuildID("EPlPwC3MJFgg3YYfTGwl")))
	require.False(t, isGoModuleIndex([]byte("go object linux amd64 go1.24.7\n")))
	require.False(t, isGoModuleIndex([]byte("go index"))) // no version letter
	require.False(t, isGoModuleIndex([]byte("not an index")))
	require.False(t, isGoModuleIndex(nil))
	require.False(t, isGoModuleIndex([]byte{}))
}

// TestWebBackend_GetRefusesModuleIndex is the regression test for the
// "package runtime is not in std" CI failure: a Go module index blob served
// from the shared cache cannot be proven to belong under the requested action
// key (it carries no build id and the outputID hash only proves self
// consistency), and a wrong one is fatal at package load. So even a body that
// passes the outputID integrity gate must be refused as a miss when it is a
// module index, letting cmd/go recompute it locally. The key is also evicted.
func TestWebBackend_GetRefusesModuleIndex(t *testing.T) {
	const actionID = "aabbccdd11223344"
	// A well-formed index whose body DOES hash to its advertised outputID, so it
	// sails through outputIDMatches and buildIDMatchesAction — only the
	// module-index guard can stop it.
	index := "go index v2\n" + largePayload(2048)
	outputID := testOutputID(index)

	objectPath := "/testbucket/go-buildcache/v1" + actionID
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != objectPath {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("X-Amz-Meta-Outputid", outputID)
		w.WriteHeader(200)
		c, _ := compressData([]byte(index))
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
	require.True(t, miss, "a module-index blob must be refused, never served")
	require.Equal(t, uint32(1), b.MissModuleIndex.Load())
	require.Equal(t, uint32(0), b.Stats.Hits.Load())
	require.Equal(t, uint32(0), b.MissChecksum.Load(), "the body is valid; it is refused for being an index, not for corruption")
	require.False(t, contains(), "the key must be evicted so a local recompute is free to re-Put")
}

// TestWebBackend_PutRefusesModuleIndex verifies the write side: go-toolchain
// never publishes a module index to the shared cache (the read side refuses all
// of them, so an upload is pure downside and a standing poison vector). The
// upload is dropped before any HTTP request and the optimistic index claim is
// released.
func TestWebBackend_PutRefusesModuleIndex(t *testing.T) {
	const actionID = "aabbccdd11223344"
	index := "go index v2\n" + largePayload(2048) // large enough to take the individual path
	outputID := testOutputID(index)

	var puts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			puts.Add(1)
			io.ReadAll(r.Body)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	b, err := NewWebBackend(WebConfig{
		Bucket: "testbucket", Endpoint: srv.URL,
		AccessKey: "testkey", SecretKey: "testsecret",
	})
	require.NoError(t, err)
	defer b.Close()

	err = b.Put(actionID, outputID, nopReader(index), int64(len(index)))
	require.NoError(t, err, "a refused upload is not an error")
	require.Equal(t, int64(0), puts.Load(), "no PUT must reach the remote for a module index")
	require.Equal(t, uint32(0), b.Stats.Puts.Load())

	b.keysMu.RLock()
	claimed := b.keys.Contains(b.key(actionID))
	b.keysMu.RUnlock()
	require.False(t, claimed, "the index claim must be released so the key is not marked present")
}

// TestWireBatchCallbacks_SkipsModuleIndex verifies the prefetch path does not
// seed a module index into the local store: a mis-keyed index materialized as a
// local hit would break the build just as a served one does. A non-index entry
// alongside it still populates, proving the skip is specific to the index.
func TestWireBatchCallbacks_SkipsModuleIndex(t *testing.T) {
	local, err := NewLocalCache(t.TempDir())
	require.NoError(t, err)
	defer local.Close()

	wb := &WebBackend{prefix: "go-buildcache/"}
	wireBatchCallbacks(wb, local, noopSink{})

	index := "go index v2\n" + largePayload(64)
	indexCompressed, _ := compressData([]byte(index))
	plain := "an ordinary cached body"
	plainCompressed, _ := compressData([]byte(plain))

	aidIndex := strings.Repeat("a", 64)
	aidPlain := strings.Repeat("c", 64)
	wb.OnBatchEntries([]BatchEntry{
		{Key: "go-buildcache/v1" + aidIndex, OutputID: testOutputID(index), Data: indexCompressed, Prefetch: true},
		{Key: "go-buildcache/v1" + aidPlain, OutputID: testOutputID(plain), Data: plainCompressed, Prefetch: true},
	})

	_, missIndex := local.Get(aidIndex)
	require.True(t, missIndex, "a module index must never be prefetched into the local store")
	_, missPlain := local.Get(aidPlain)
	require.False(t, missPlain, "an ordinary entry must still be prefetched")
}
