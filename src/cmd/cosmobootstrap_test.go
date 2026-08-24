package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupCosmoTest neutralizes every external dependency of
// EnsureCosmoToolchain: env vars, the version probe, the host platform, and
// the cache directory. Individual tests override the pieces they exercise.
func setupCosmoTest(t *testing.T) (cacheDir string) {
	t.Helper()
	t.Setenv(cosmoGorootEnv, "")
	t.Setenv(cosmoBranchEnv, "")

	cacheDir = t.TempDir()
	oldCacheDir := goCacheDirFunc
	goCacheDirFunc = func() (string, error) { return cacheDir, nil }

	oldVersion := cosmoGoVersionFunc
	cosmoGoVersionFunc = func(root string) (string, error) { return "go version go1.26.4cosmo linux/amd64", nil }

	oldHost := cosmoHostPlatformFunc
	cosmoHostPlatformFunc = func() (string, string) { return "linux", "amd64" }

	oldBase := cosmoDownloadBase
	t.Cleanup(func() {
		goCacheDirFunc = oldCacheDir
		cosmoGoVersionFunc = oldVersion
		cosmoHostPlatformFunc = oldHost
		cosmoDownloadBase = oldBase
	})
	return cacheDir
}

// makeCosmoTarball builds an in-memory tar.gz with the gosmopolitan layout
// (top-level go/ directory containing bin/go).
func makeCosmoTarball(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "go/", Typeflag: tar.TypeDir, Mode: 0755}))
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "go/bin/", Typeflag: tar.TypeDir, Mode: 0755}))
	content := []byte("#!/bin/sh\necho fake cosmo go\n")
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "go/bin/go", Typeflag: tar.TypeReg, Mode: 0755, Size: int64(len(content))}))
	_, err := tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func TestEnsureCosmoToolchainEnvGoroot(t *testing.T) {
	setupCosmoTest(t)

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bin"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "bin", "go"), []byte("fake"), 0755))
	t.Setenv(cosmoGorootEnv, root)

	got, err := EnsureCosmoToolchain()
	require.NoError(t, err)
	assert.Equal(t, root, got)
}

func TestEnsureCosmoToolchainEnvGorootMissingBinGo(t *testing.T) {
	setupCosmoTest(t)

	root := t.TempDir() // no bin/go inside
	t.Setenv(cosmoGorootEnv, root)

	_, err := EnsureCosmoToolchain()
	require.Error(t, err)
	assert.Contains(t, err.Error(), cosmoGorootEnv)
	assert.Contains(t, err.Error(), "bin/go")
}

func TestEnsureCosmoToolchainEnvGorootBrokenVersionProbe(t *testing.T) {
	setupCosmoTest(t)

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bin"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "bin", "go"), []byte("fake"), 0755))
	t.Setenv(cosmoGorootEnv, root)
	cosmoGoVersionFunc = func(string) (string, error) { return "", fmt.Errorf("go version failed: boom") }

	_, err := EnsureCosmoToolchain()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "go version failed")
}

func TestEnsureCosmoToolchainUnsupportedHost(t *testing.T) {
	setupCosmoTest(t)
	cosmoHostPlatformFunc = func() (string, string) { return "darwin", "arm64" }

	_, err := EnsureCosmoToolchain()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "darwin/arm64")
	assert.Contains(t, err.Error(), cosmoGorootEnv)
}

func TestEnsureCosmoToolchainDownloadsAndCaches(t *testing.T) {
	cacheDir := setupCosmoTest(t)
	tarball := makeCosmoTarball(t)

	var probes, downloads atomic.Int32
	var gotQuery atomic.Value
	mux := http.NewServeMux()
	mux.HandleFunc("/gosmopolitan", func(w http.ResponseWriter, r *http.Request) {
		gotQuery.Store(r.URL.RawQuery)
		if r.Method == http.MethodHead {
			probes.Add(1)
			w.Header().Set("Location", "/static?project=gosmopolitan&v=42&os=linux&arch=amd64")
			w.WriteHeader(http.StatusMovedPermanently)
			return
		}
		downloads.Add(1)
		http.Redirect(w, r, "/static?project=gosmopolitan&v=42&os=linux&arch=amd64", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/static", func(w http.ResponseWriter, r *http.Request) {
		w.Write(tarball)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cosmoDownloadBase = srv.URL + "/gosmopolitan"

	got, err := EnsureCosmoToolchain()
	require.NoError(t, err)

	// Cached under the buildhost redirect version (v42), using the tarball's top-level go/ dir as GOROOT.
	assert.Equal(t, filepath.Join(cacheDir, "cosmo", "v42", "go"), got)
	assert.FileExists(t, filepath.Join(got, "bin", "go"))
	assert.Equal(t, int32(1), downloads.Load())
	assert.Contains(t, gotQuery.Load().(string), "branch=master")
	assert.Contains(t, gotQuery.Load().(string), "os=linux")
	assert.Contains(t, gotQuery.Load().(string), "arch=amd64")

	// Second resolution: cache hit, no re-download.
	got2, err := EnsureCosmoToolchain()
	require.NoError(t, err)
	assert.Equal(t, got, got2)
	assert.Equal(t, int32(1), downloads.Load(), "cached toolchain must not be re-downloaded")
}

func TestEnsureCosmoToolchainBranchEnvSelectsBranch(t *testing.T) {
	setupCosmoTest(t)
	t.Setenv(cosmoBranchEnv, "claude/some-branch")
	tarball := makeCosmoTarball(t)

	var gotQuery atomic.Value
	mux := http.NewServeMux()
	mux.HandleFunc("/gosmopolitan", func(w http.ResponseWriter, r *http.Request) {
		gotQuery.Store(r.URL.RawQuery)
		if r.Method == http.MethodHead {
			w.Header().Set("Location", "/static?v=7")
			w.WriteHeader(http.StatusMovedPermanently)
			return
		}
		w.Write(tarball)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cosmoDownloadBase = srv.URL + "/gosmopolitan"

	got, err := EnsureCosmoToolchain()
	require.NoError(t, err)
	assert.Contains(t, got, filepath.Join("cosmo", "v7", "go"))
	assert.Contains(t, gotQuery.Load().(string), "branch=claude%2Fsome-branch")
}

func TestEnsureCosmoToolchainFallsBackToBranchKey(t *testing.T) {
	cacheDir := setupCosmoTest(t)
	tarball := makeCosmoTarball(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/gosmopolitan", func(w http.ResponseWriter, r *http.Request) {
		// No redirect: serves the tarball directly, so the version probe finds no Location header.
		w.Write(tarball)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cosmoDownloadBase = srv.URL + "/gosmopolitan"

	got, err := EnsureCosmoToolchain()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(cacheDir, "cosmo", "branch-master", "go"), got)
	assert.FileExists(t, filepath.Join(got, "bin", "go"))
}

func TestEnsureCosmoToolchainDownloadHTTPError(t *testing.T) {
	setupCosmoTest(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	cosmoDownloadBase = srv.URL + "/gosmopolitan"

	_, err := EnsureCosmoToolchain()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 404")
	// The error must name the env overrides that unblock the user.
	assert.Contains(t, err.Error(), cosmoGorootEnv)
	assert.Contains(t, err.Error(), cosmoBranchEnv)
}

func TestEnsureCosmoToolchainRejectsArchiveWithoutGo(t *testing.T) {
	setupCosmoTest(t)

	// A valid tar.gz that lacks go/bin/go.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "go/", Typeflag: tar.TypeDir, Mode: 0755}))
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(buf.Bytes())
	}))
	defer srv.Close()
	cosmoDownloadBase = srv.URL + "/gosmopolitan"

	_, err := EnsureCosmoToolchain()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "go/bin/go")
}

func TestSanitizeCacheKey(t *testing.T) {
	assert.Equal(t, "claude-some-branch", sanitizeCacheKey("claude/some-branch"))
	assert.Equal(t, "v1.2.3", sanitizeCacheKey("v1.2.3"))
	assert.Equal(t, "a-b-c", sanitizeCacheKey("a b:c"))
}
