package cmd

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A reset mid-stream is the network rather than an answer, so the build retries
// instead of ending with no compiler at all.
func TestADroppedDownloadIsRetriedUntilItLands(t *testing.T) {
	t.Serial()
	prev := cosmoRetryInterval
	cosmoRetryInterval = time.Millisecond
	t.Cleanup(func() { cosmoRetryInterval = prev })

	tries := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tries++
		if tries < 3 {
			// A body that stops early is what a reset looks like to the reader.
			w.Header().Set("Content-Length", "4096")
			w.Write([]byte("not a tarball"))
			return
		}
		w.Write(makeCosmoTarballNamed(t, filepath.Base(cosmoGoBinPath("x"))))
	}))
	defer srv.Close()

	cache := t.TempDir()
	require.NoError(t, downloadCosmoToolchain(srv.URL, cache, "v1"))
	assert.Equal(t, 3, tries, "it kept trying rather than giving up")
	assert.FileExists(t, cosmoGoBinPath(filepath.Join(cache, "v1", "go")), "and the tree landed under the key")
}

// buildhost saying it publishes nothing is an answer, so retrying it would spin
// forever on a question already settled.
func TestANotFoundStopsAtOnce(t *testing.T) {
	tries := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tries++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	err := downloadCosmoToolchain(srv.URL, t.TempDir(), "v1")
	require.Error(t, err)
	assert.Equal(t, 1, tries, "it asked once")
	assert.Contains(t, err.Error(), "404")
	var terminal terminalDownloadError
	assert.True(t, errors.As(err, &terminal), "and said the answer is final")
}
