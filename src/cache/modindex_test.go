package cache

import (
	"bytes"
	"crypto/sha256"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
		w.Header().Set("X-Cache-Meta-Outputid", outputID)
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

// TestServer_PutRefusesModuleIndexAndKeepsDiskPath is the local-tier half of
// the refusal: a PUT carrying a module index stores nothing, yet still answers
// with a DiskPath naming a file that holds the body -- cmd/go rejects an empty
// DiskPath outright ("GOCACHEPROG didn't return DiskPath in put response") and
// treats a failed index store as fatal, so a refusal that errored or replied
// empty would break every build instead of the one in five it was fixing.
func TestServer_PutRefusesModuleIndexAndKeepsDiskPath(t *testing.T) {
	lc, err := NewLocalCache(t.TempDir())
	require.NoError(t, err)
	defer lc.Close()
	srv := NewServer(lc, nil)

	index := "go index v2\n" + largePayload(2048)
	action := []byte{0xaa, 0xbb, 0xcc, 0xdd, 0x00, 0x11, 0x22, 0x33}
	sum := sha256.Sum256([]byte(index))

	resp := srv.handlePut(Request{
		ID: 1, Command: CmdPut, ActionID: action, OutputID: sum[:],
		Body: []byte(index), BodySize: int64(len(index)),
	})
	require.Empty(t, resp.Err, "a refused index PUT is not a protocol error")
	require.NotEmpty(t, resp.DiskPath, "the put reply still owes cmd/go a DiskPath")
	onDisk, err := os.ReadFile(resp.DiskPath)
	require.NoError(t, err, "the DiskPath must name a real file until close")
	require.Equal(t, index, string(onDisk), "the sunk file must hold the body cmd/go handed over")
	require.Equal(t, uint32(1), srv.IndexPutsRefused.Load())

	// Nothing entered the cache, so the next GET misses and cmd/go recomputes.
	_, miss := lc.Peek(srv.actionKey(action))
	require.True(t, miss, "a module index must never enter the local store")
	require.True(t, srv.handleGet(Request{ID: 2, Command: CmdGet, ActionID: action}).Miss)

	// The sink is scratch, not cache: it goes away when the protocol loop ends.
	srv.removeIndexSink()
	_, err = os.Stat(resp.DiskPath)
	require.True(t, os.IsNotExist(err), "the sink must be removed at close")
}

// TestServer_NeverStoresModuleIndexBlob is the deterministic assertion the
// 1-in-5 type-check flake needs: no run of the cacheprog may leave a module
// index anywhere under the cache root. A wrong index served back under an
// action key that hashes NO content renames a package at load time (otel's
// attribute package arriving named "trace"), and nothing downstream can detect
// it -- so the property to pin is that the blob is never stored at all, not
// that some later gate catches it.
func TestServer_NeverStoresModuleIndexBlob(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.NoError(t, err)

	index := "go index v2\n" + largePayload(512)
	indexSum := sha256.Sum256([]byte(index))
	plain := "an ordinary cached body"
	plainSum := sha256.Sum256([]byte(plain))
	indexAction := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	plainAction := []byte{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18}

	var input strings.Builder
	input.WriteString(makePutRequest(Request{
		ID: 1, Command: CmdPut, ActionID: indexAction, OutputID: indexSum[:],
		BodySize: int64(len(index)),
	}, index))
	input.WriteString(makePutRequest(Request{
		ID: 2, Command: CmdPut, ActionID: plainAction, OutputID: plainSum[:],
		BodySize: int64(len(plain)),
	}, plain))
	input.WriteString(makeRequest(Request{ID: 3, Command: CmdGet, ActionID: indexAction}))
	input.WriteString(makeRequest(Request{ID: 4, Command: CmdGet, ActionID: plainAction}))
	input.WriteString(makeRequest(Request{ID: 5, Command: CmdClose}))

	var out bytes.Buffer
	srv := NewServer(lc, nil)
	require.NoError(t, srv.Run(strings.NewReader(input.String()), &out))

	byID := map[int64]Response{}
	for _, r := range parseResponses(t, out.Bytes()) {
		byID[r.ID] = r
	}
	require.Empty(t, byID[1].Err)
	require.NotEmpty(t, byID[1].DiskPath)
	require.True(t, byID[3].Miss, "the index key must miss so cmd/go recomputes")
	require.False(t, byID[4].Miss, "an ordinary body is still cached and served")

	// The whole cache root, every tier and every sidecar: no index bytes.
	require.NoError(t, filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		head, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		require.False(t, isGoModuleIndex(head), "module index stored at %s", path)
		return nil
	}))
}

// TestLocalServeGatesRefuseModuleIndex covers the residue case: an index a
// PREVIOUS binary stored (both tiers served them then) must not be handed back
// by this one. The PUT refusal is what keeps this from becoming the permanent
// accept-at-Put/refuse-at-Get miss loop the file-top comment in verify.go
// describes.
func TestLocalServeGatesRefuseModuleIndex(t *testing.T) {
	const actionID = "aabbccdd11223344"
	index := []byte("go index v2\n" + largePayload(64))
	outputID := testOutputID(string(index))

	reason, ok := verifyBodyForServe(actionID, outputID, index)
	require.False(t, ok, "the loose tier must refuse a stored module index")
	require.Contains(t, reason, "module index")

	// The pack tier decides from memoized facts, so the index-ness of the body
	// has to survive memoization.
	vi := verifyInfoForPut(outputID, index)
	require.True(t, vi.isModIndex)
	require.False(t, vi.servableForAction(actionID))

	// An ordinary body under the same key is unaffected.
	plain := []byte("an ordinary cached body")
	_, ok = verifyBodyForServe(actionID, testOutputID(string(plain)), plain)
	require.True(t, ok)
	require.True(t, verifyInfoForPut(testOutputID(string(plain)), plain).servableForAction(actionID))
}
