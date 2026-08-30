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
	t.Setenv(cosmoVersionEnv, "")

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
	return makeCosmoTarballNamed(t, "go")
}

// makeCosmoTarballNamed builds the distribution archive with the go binary
// under binName, which is how a windows distribution spells it (go.exe).
func makeCosmoTarballNamed(t *testing.T, binName string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "go/", Typeflag: tar.TypeDir, Mode: 0755}))
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "go/bin/", Typeflag: tar.TypeDir, Mode: 0755}))
	content := []byte("#!/bin/sh\necho fake cosmo go\n")
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "go/bin/" + binName, Typeflag: tar.TypeReg, Mode: 0755, Size: int64(len(content))}))
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

// Every host asks buildhost for its own os/arch. No host list lives here: a
// went stale and refused darwin/arm64 while buildhost served it.
func TestEnsureCosmoToolchainDownloadsForEveryHost(t *testing.T) {
	for _, host := range []struct{ goos, goarch string }{
		{"linux", "amd64"},
		{"darwin", "arm64"},
		{"darwin", "amd64"},
		{"windows", "amd64"},
		{"linux", "arm64"},
	} {
		t.Run(host.goos+"/"+host.goarch, func(t *testing.T) {
			cacheDir := setupCosmoTest(t)
			cosmoHostPlatformFunc = func() (string, string) { return host.goos, host.goarch }
			// Serve the archive this host's own distribution carries; windows names the binary go.exe.
			goBinName := "go"
			if host.goos == "windows" {
				goBinName += ".exe"
			}
			tarball := makeCosmoTarballNamed(t, goBinName)

			var gotQuery atomic.Value
			mux := http.NewServeMux()
			mux.HandleFunc("/gosmopolitan", func(w http.ResponseWriter, r *http.Request) {
				gotQuery.Store(r.URL.RawQuery)
				if r.Method == http.MethodHead {
					w.Header().Set("Location", "/static?project=gosmopolitan&v=42")
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
			assert.Equal(t, filepath.Join(cacheDir, "cosmo", "v42", "go"), got)
			assert.Contains(t, gotQuery.Load().(string), "os="+host.goos)
			assert.Contains(t, gotQuery.Load().(string), "arch="+host.goarch)
		})
	}
}

// A host buildhost has no toolchain for gets buildhost's own answer, named as
// such, plus the local-GOROOT escape -- never a refusal from a list here.
func TestEnsureCosmoToolchainUnpublishedHostNamesTheEscape(t *testing.T) {
	setupCosmoTest(t)
	cosmoHostPlatformFunc = func() (string, string) { return "plan9", "386" }

	mux := http.NewServeMux()
	mux.HandleFunc("/gosmopolitan", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cosmoDownloadBase = srv.URL + "/gosmopolitan"

	_, err := EnsureCosmoToolchain()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "os=plan9")
	assert.Contains(t, err.Error(), "publishes no gosmopolitan toolchain for this host")
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

	// The repeat resolution: cache hit, no re-download.
	got2, err := EnsureCosmoToolchain()
	require.NoError(t, err)
	assert.Equal(t, got, got2)
	assert.Equal(t, int32(1), downloads.Load(), "cached toolchain must not be re-downloaded")
}

// A windows distribution names the go binary go.exe, so the whole resolution
// has to agree on the suffix. A path spelled by hand rejects a good archive
// here, and misses the cache on every later run.
func TestEnsureCosmoToolchainWindowsHostUsesExeSuffix(t *testing.T) {
	cacheDir := setupCosmoTest(t)
	cosmoHostPlatformFunc = func() (string, string) { return "windows", "amd64" }
	tarball := makeCosmoTarballNamed(t, "go.exe")

	var downloads atomic.Int32
	var gotQuery atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery.Store(r.URL.RawQuery)
		if r.Method == http.MethodHead {
			w.Header().Set("Location", "/static?v=42")
			w.WriteHeader(http.StatusMovedPermanently)
			return
		}
		downloads.Add(1)
		w.Write(tarball)
	}))
	defer srv.Close()
	cosmoDownloadBase = srv.URL + "/gosmopolitan"

	got, err := EnsureCosmoToolchain()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(cacheDir, "cosmo", "v42", "go"), got)
	assert.Contains(t, gotQuery.Load().(string), "os=windows")
	assert.Equal(t, filepath.Join(got, "bin", "go.exe"), cosmoGoBinPath(got))
	assert.FileExists(t, cosmoGoBinPath(got))

	got2, err := EnsureCosmoToolchain()
	require.NoError(t, err)
	assert.Equal(t, got, got2)
	assert.Equal(t, int32(1), downloads.Load(), "the cache hit must look for go.exe rather than re-download")
}

// The rejection names the file it wanted, which is the suffix on a windows host.
func TestEnsureCosmoToolchainWindowsHostRejectsArchiveWithoutExe(t *testing.T) {
	setupCosmoTest(t)
	cosmoHostPlatformFunc = func() (string, string) { return "windows", "amd64" }
	tarball := makeCosmoTarball(t) // a unix distribution: go/bin/go

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tarball)
	}))
	defer srv.Close()
	cosmoDownloadBase = srv.URL + "/gosmopolitan"

	_, err := EnsureCosmoToolchain()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "go/bin/go.exe")
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
