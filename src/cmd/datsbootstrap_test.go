package cmd

import (
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

// setupDatsTest neutralizes every external dependency of EnsureDats: env
// vars, the version probe, the host platform, and the cache directory.
// Individual tests override the pieces they exercise.
func setupDatsTest(t *testing.T) (cacheDir string) {
	t.Helper()
	t.Setenv(datsBinEnv, "")
	t.Setenv(datsBranchEnv, "")

	cacheDir = t.TempDir()
	oldCacheDir := goCacheDirFunc
	goCacheDirFunc = func() (string, error) { return cacheDir, nil }

	oldVersion := datsVersionFunc
	datsVersionFunc = func(bin string) (string, error) { return "dats v0.0.0-test", nil }

	oldHost := datsHostPlatformFunc
	datsHostPlatformFunc = func() (string, string) { return "linux", "amd64" }

	oldBase := datsDownloadBase
	t.Cleanup(func() {
		goCacheDirFunc = oldCacheDir
		datsVersionFunc = oldVersion
		datsHostPlatformFunc = oldHost
		datsDownloadBase = oldBase
	})
	return cacheDir
}

func TestEnsureDatsEnvBin(t *testing.T) {
	setupDatsTest(t)

	bin := filepath.Join(t.TempDir(), "dats")
	require.NoError(t, os.WriteFile(bin, []byte("fake"), 0o755))
	t.Setenv(datsBinEnv, bin)

	got, err := EnsureDats()
	require.NoError(t, err)
	assert.Equal(t, bin, got)
}

func TestEnsureDatsEnvBinBrokenVersionProbe(t *testing.T) {
	setupDatsTest(t)

	bin := filepath.Join(t.TempDir(), "dats")
	require.NoError(t, os.WriteFile(bin, []byte("fake"), 0o755))
	t.Setenv(datsBinEnv, bin)
	datsVersionFunc = func(string) (string, error) { return "", fmt.Errorf("dats version failed: boom") }

	_, err := EnsureDats()
	require.Error(t, err)
	assert.Contains(t, err.Error(), datsBinEnv)
	assert.Contains(t, err.Error(), "dats version failed")
}

func TestEnsureDatsDownloadsAndCaches(t *testing.T) {
	cacheDir := setupDatsTest(t)

	body := []byte("#!/bin/sh\necho fake dats\n")
	var downloads atomic.Int32
	var gotQuery atomic.Value
	mux := http.NewServeMux()
	mux.HandleFunc("/dats", func(w http.ResponseWriter, r *http.Request) {
		gotQuery.Store(r.URL.RawQuery)
		if r.Method == http.MethodHead {
			w.Header().Set("Location", "/static?project=dats&v=43&os=linux&arch=amd64")
			w.WriteHeader(http.StatusMovedPermanently)
			return
		}
		downloads.Add(1)
		w.Write(body)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	datsDownloadBase = srv.URL + "/dats"

	got, err := EnsureDats()
	require.NoError(t, err)

	// Cached under the buildhost version from the redirect (v43), as a raw
	// executable binary (no archive).
	assert.Equal(t, filepath.Join(cacheDir, "dats", "v43", "dats"), got)
	data, err := os.ReadFile(got)
	require.NoError(t, err)
	assert.Equal(t, body, data)
	info, err := os.Stat(got)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&0o111, "downloaded dats must be executable")
	assert.Equal(t, int32(1), downloads.Load())
	assert.Contains(t, gotQuery.Load().(string), "branch=master")
	assert.Contains(t, gotQuery.Load().(string), "os=linux")
	assert.Contains(t, gotQuery.Load().(string), "arch=amd64")

	// No leftover temp files next to the binary.
	entries, err := os.ReadDir(filepath.Dir(got))
	require.NoError(t, err)
	assert.Len(t, entries, 1)

	// Second resolution: cache hit, no re-download.
	got2, err := EnsureDats()
	require.NoError(t, err)
	assert.Equal(t, got, got2)
	assert.Equal(t, int32(1), downloads.Load(), "cached dats must not be re-downloaded")
}

func TestEnsureDatsBranchEnvSelectsBranch(t *testing.T) {
	setupDatsTest(t)
	t.Setenv(datsBranchEnv, "claude/some-branch")

	var gotQuery atomic.Value
	mux := http.NewServeMux()
	mux.HandleFunc("/dats", func(w http.ResponseWriter, r *http.Request) {
		gotQuery.Store(r.URL.RawQuery)
		if r.Method == http.MethodHead {
			w.Header().Set("Location", "/static?v=7")
			w.WriteHeader(http.StatusMovedPermanently)
			return
		}
		w.Write([]byte("fake dats"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	datsDownloadBase = srv.URL + "/dats"

	got, err := EnsureDats()
	require.NoError(t, err)
	assert.Contains(t, got, filepath.Join("dats", "v7", "dats"))
	assert.Contains(t, gotQuery.Load().(string), "branch=claude%2Fsome-branch")
}

func TestEnsureDatsFallsBackToBranchKey(t *testing.T) {
	cacheDir := setupDatsTest(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No redirect: the endpoint serves the binary directly, so the
		// version probe finds no Location to parse.
		w.Write([]byte("fake dats"))
	}))
	defer srv.Close()
	datsDownloadBase = srv.URL + "/dats"

	got, err := EnsureDats()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(cacheDir, "dats", "branch-master", "dats"), got)
	assert.FileExists(t, got)
}

func TestEnsureDatsDownloadHTTPError(t *testing.T) {
	setupDatsTest(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	datsDownloadBase = srv.URL + "/dats"

	_, err := EnsureDats()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 404")
	// The error must name the env overrides that unblock the user.
	assert.Contains(t, err.Error(), datsBinEnv)
	assert.Contains(t, err.Error(), datsBranchEnv)
}

func TestEnsureDatsDownloadFailedVersionProbe(t *testing.T) {
	setupDatsTest(t)
	datsVersionFunc = func(string) (string, error) { return "", fmt.Errorf("exec format error") }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not a real binary"))
	}))
	defer srv.Close()
	datsDownloadBase = srv.URL + "/dats"

	_, err := EnsureDats()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed its version probe")
}

func TestEnsureDatsCachedBinaryBroken(t *testing.T) {
	cacheDir := setupDatsTest(t)
	datsVersionFunc = func(string) (string, error) { return "", fmt.Errorf("exec format error") }

	// Pre-populate the branch-keyed cache slot (the probe target below never
	// redirects, so keying falls back to the branch).
	binPath := filepath.Join(cacheDir, "dats", "branch-master", "dats")
	require.NoError(t, os.MkdirAll(filepath.Dir(binPath), 0o755))
	require.NoError(t, os.WriteFile(binPath, []byte("corrupt"), 0o755))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fake dats"))
	}))
	defer srv.Close()
	datsDownloadBase = srv.URL + "/dats"

	_, err := EnsureDats()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is broken")
	assert.Contains(t, err.Error(), datsBinEnv)
}

func TestEnsureDatsWindowsBinaryName(t *testing.T) {
	cacheDir := setupDatsTest(t)
	datsHostPlatformFunc = func() (string, string) { return "windows", "amd64" }

	var gotQuery atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery.Store(r.URL.RawQuery)
		w.Write([]byte("fake dats.exe"))
	}))
	defer srv.Close()
	datsDownloadBase = srv.URL + "/dats"

	got, err := EnsureDats()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(cacheDir, "dats", "branch-master", "dats.exe"), got)
	assert.Contains(t, gotQuery.Load().(string), "os=windows")
}
