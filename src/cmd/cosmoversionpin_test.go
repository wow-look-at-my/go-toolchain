package cmd

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCosmoDownloadURLPinReplacesTheBranch(t *testing.T) {
	t.Parallel()
	// buildhost reads v and branch as alternatives, so a pin drops the branch.
	unpinned := cosmoDownloadURL("master", "", "linux", "amd64")
	assert.Equal(t, "https://dl.pazer.build/gosmopolitan?branch=master&os=linux&arch=amd64", unpinned)

	for _, pin := range []string{"372", "v372", " v372 "} {
		got := cosmoDownloadURL("master", pin, "windows", "amd64")
		assert.Equal(t, "https://dl.pazer.build/gosmopolitan?v=372&os=windows&arch=amd64", got, "pin %q", pin)
	}
}

func TestCosmoCacheKeyForPinNeedsNoProbe(t *testing.T) {
	t.Parallel()
	const dead = "http://127.0.0.1:1/gosmopolitan?v=372&os=linux&arch=amd64"
	// Nothing answers a probe there, so naming the release proves the pin did.
	assert.Equal(t, "v372", cosmoCacheKeyFor(dead, "master", "v372"))
	// Control: unpinned, the same address yields only the branch key.
	assert.Equal(t, "branch-master", cosmoCacheKeyFor(dead, "master", ""))
}

func TestResolveCosmoVersionEchoesThePin(t *testing.T) {
	setupCosmoTest(t)
	t.Setenv(cosmoVersionEnv, "372")
	assert.Equal(t, "v372", ResolveCosmoVersion())
}

func TestResolveCosmoVersionReadsTheRedirect(t *testing.T) {
	setupCosmoTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "master", r.URL.Query().Get("branch"), "an unpinned resolve selects by branch")
		w.Header().Set("Location", "https://static.pazer.build/file?project=gosmopolitan&v=372&os=linux&arch=amd64")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()
	cosmoDownloadBase = srv.URL + "/gosmopolitan"

	assert.Equal(t, "v372", ResolveCosmoVersion())
}

func TestEnsureCosmoToolchainDownloadsThePinnedRelease(t *testing.T) {
	setupCosmoTest(t)
	t.Setenv(cosmoVersionEnv, "v372")

	tarball := makeCosmoTarball(t)
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.RawQuery
		w.Write(tarball) //nolint:errcheck
	}))
	defer srv.Close()
	cosmoDownloadBase = srv.URL + "/gosmopolitan"

	root, err := EnsureCosmoToolchain()
	require.NoError(t, err)
	assert.Contains(t, root, "v372", "the pin names the cache directory")
	assert.Contains(t, asked, "v=372")
	assert.NotContains(t, asked, "branch=", "a pinned request must not also select a branch")
}
