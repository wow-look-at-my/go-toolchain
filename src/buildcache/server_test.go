package buildcache

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-s3-server/cacheclient"
)

const (
	testBucket   = "gobuildcache"
	testUsername = "owner-test"
	testPassword = "owner-test-password"
)

// cacheServer speaks enough of the build cache protocol for a client to load
// an empty key index, miss every lookup and store.
type cacheServer struct {
	*httptest.Server

	mu        sync.Mutex
	indexGets int
	requests  int
	builds    set.Set[string]
}

func newCacheServer(t *testing.T) *cacheServer {
	t.Helper()
	srv := &cacheServer{builds: set.New[string]()}
	srv.Server = httptest.NewServer(http.HandlerFunc(srv.serve))
	t.Cleanup(srv.Close)
	return srv
}

// config answers the GO_BUILDCACHE_CONFIG value naming this server.
func (srv *cacheServer) config(t *testing.T) string {
	t.Helper()
	raw, err := json.Marshal(map[string]string{
		"endpoint": srv.URL,
		"bucket":   testBucket,
		"username": testUsername,
		"password": testPassword,
	})
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(raw)
}

func (srv *cacheServer) serve(w http.ResponseWriter, r *http.Request) {
	io.Copy(io.Discard, r.Body)
	user, pass, ok := r.BasicAuth()
	if !ok || user != testUsername || pass != testPassword {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	srv.mu.Lock()
	srv.requests++
	srv.builds.Add(r.Header.Get(cacheclient.HeaderBuild))
	srv.mu.Unlock()

	name := strings.TrimPrefix(r.URL.Path, "/"+testBucket+"/")
	switch {
	case name == "_index" && r.Method == http.MethodGet:
		srv.mu.Lock()
		srv.indexGets++
		srv.mu.Unlock()
		blob := emptyIndexBlob()
		w.Header().Set("ETag", `"`+hex.EncodeToString(blob[len(blob)-sha256.Size:])+`"`)
		w.Write(blob)
	case name == cacheclient.SkipReportPath:
		w.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(name, "_batch/"):
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodPut:
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// snapshot answers the index downloads, the requests, and the distinct
// client processes the server has seen.
func (srv *cacheServer) snapshot() (indexGets, requests, clients int) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	return srv.indexGets, srv.requests, srv.builds.Len()
}

// emptyIndexBlob is a GBCI v1 key index holding no keys: the header, then
// the sha256 of everything before the trailer.
func emptyIndexBlob() []byte {
	const headerSize = 24
	blob := make([]byte, headerSize+sha256.Size)
	copy(blob[0:4], "GBCI")
	blob[4] = 1
	blob[5] = 32
	binary.LittleEndian.PutUint64(blob[16:24], 0)
	digest := sha256.Sum256(blob[:headerSize])
	copy(blob[headerSize:], digest[:])
	return blob
}
