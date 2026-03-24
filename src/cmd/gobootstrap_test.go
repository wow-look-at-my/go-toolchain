package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestRequiredGoVersion(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("go.mod", []byte("module test\n\ngo 1.24.11\n"), 0644)
	v, err := requiredGoVersion()
	assert.Nil(t, err)
	assert.Equal(t, "1.24.11", v)
}

func TestRequiredGoVersionToolchainDirective(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("go.mod", []byte("module test\n\ngo 1.24.0\n\ntoolchain go1.25.0\n"), 0644)
	v, err := requiredGoVersion()
	assert.Nil(t, err)
	assert.Equal(t, "1.25.0", v) // toolchain directive takes precedence
}

func TestRequiredGoVersionTwoParts(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("go.mod", []byte("module test\n\ngo 1.25\n"), 0644)
	v, err := requiredGoVersion()
	assert.Nil(t, err)
	assert.Equal(t, "1.25.0", v)
}

func TestNormalizeGoVersion(t *testing.T) {
	assert.Equal(t, "1.25.0", normalizeGoVersion("1.25"))
	assert.Equal(t, "1.24.11", normalizeGoVersion("1.24.11"))
	assert.Equal(t, "1.25.1", normalizeGoVersion("1.25.1"))
}

func TestRequiredGoVersionNoGoDirective(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("go.mod", []byte("module test\n"), 0644)
	v, err := requiredGoVersion()
	assert.Nil(t, err)
	assert.Equal(t, "", v)
}

func TestRequiredGoVersionNoMod(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	v, err := requiredGoVersion()
	assert.NotNil(t, err)
	assert.Equal(t, "", v)
}

func TestInstalledGoVersion(t *testing.T) {
	v, err := installedGoVersion()
	assert.Nil(t, err)
	assert.NotEmpty(t, v)
	// Should be parseable as semver
	assert.Contains(t, v, ".")
}

func TestGoDownloadURLsNoProxy(t *testing.T) {
	old := os.Getenv("GOPROXY_FALLBACK")
	os.Unsetenv("GOPROXY_FALLBACK")
	defer os.Setenv("GOPROXY_FALLBACK", old)

	urls := goDownloadURLs("go1.24.11.linux-amd64.tar.gz")
	assert.Equal(t, 2, len(urls))
	assert.Contains(t, urls[0], "go.dev/dl/")
	assert.Contains(t, urls[1], "dl.google.com/go/")
}

func TestGoDownloadURLsWithProxy(t *testing.T) {
	old := os.Getenv("GOPROXY_FALLBACK")
	os.Setenv("GOPROXY_FALLBACK", "https://proxy.example.com")
	defer os.Setenv("GOPROXY_FALLBACK", old)

	urls := goDownloadURLs("go1.24.11.linux-amd64.tar.gz")
	assert.Equal(t, 4, len(urls))
	assert.Contains(t, urls[2], "proxy.example.com/https://go.dev/dl/")
	assert.Contains(t, urls[3], "proxy.example.com/https://dl.google.com/go/")
}

func TestGoDownloadURLsWithTrailingSlash(t *testing.T) {
	old := os.Getenv("GOPROXY_FALLBACK")
	os.Setenv("GOPROXY_FALLBACK", "https://proxy.example.com/")
	defer os.Setenv("GOPROXY_FALLBACK", old)

	urls := goDownloadURLs("go1.24.11.linux-amd64.tar.gz")
	assert.Contains(t, urls[2], "proxy.example.com/https://go.dev/dl/")
}

func TestGoCacheDir(t *testing.T) {
	dir, err := goCacheDir()
	assert.Nil(t, err)
	assert.True(t, filepath.IsAbs(dir))
	assert.Contains(t, dir, "go-toolchain")

	// Directory should exist
	info, err := os.Stat(dir)
	assert.Nil(t, err)
	assert.True(t, info.IsDir())
}

// createTestTarGz builds a tar.gz archive in memory with the given files.
func createTestTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0755,
			Size: int64(len(content)),
		}
		require.NoError(t, tw.WriteHeader(hdr))
		_, err := tw.Write([]byte(content))
		require.Nil(t, err)
	}
	tw.Close()
	gw.Close()
	return buf.Bytes()
}

func TestExtractTarGz(t *testing.T) {
	tmpDir := t.TempDir()

	archive := createTestTarGz(t, map[string]string{
		"go/bin/go":      "#!/bin/sh\necho go",
		"go/src/main.go": "package main",
	})

	err := extractTarGz(bytes.NewReader(archive), tmpDir)
	assert.Nil(t, err)

	// Verify files were extracted
	content, err := os.ReadFile(filepath.Join(tmpDir, "go", "bin", "go"))
	assert.Nil(t, err)
	assert.Equal(t, "#!/bin/sh\necho go", string(content))

	content, err = os.ReadFile(filepath.Join(tmpDir, "go", "src", "main.go"))
	assert.Nil(t, err)
	assert.Equal(t, "package main", string(content))
}

func TestExtractTarGzWithDir(t *testing.T) {
	tmpDir := t.TempDir()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// Add a directory entry
	tw.WriteHeader(&tar.Header{Name: "go/", Typeflag: tar.TypeDir, Mode: 0755})
	tw.WriteHeader(&tar.Header{Name: "go/bin/", Typeflag: tar.TypeDir, Mode: 0755})
	// Add a regular file
	content := []byte("binary")
	tw.WriteHeader(&tar.Header{Name: "go/bin/go", Typeflag: tar.TypeReg, Mode: 0755, Size: int64(len(content))})
	tw.Write(content)
	// Add a symlink
	tw.WriteHeader(&tar.Header{Name: "go/bin/link", Typeflag: tar.TypeSymlink, Linkname: "go"})

	tw.Close()
	gw.Close()

	err := extractTarGz(bytes.NewReader(buf.Bytes()), tmpDir)
	assert.Nil(t, err)

	// Check directory
	info, err := os.Stat(filepath.Join(tmpDir, "go", "bin"))
	assert.Nil(t, err)
	assert.True(t, info.IsDir())

	// Check file
	data, err := os.ReadFile(filepath.Join(tmpDir, "go", "bin", "go"))
	assert.Nil(t, err)
	assert.Equal(t, "binary", string(data))

	// Check symlink
	target, err := os.Readlink(filepath.Join(tmpDir, "go", "bin", "link"))
	assert.Nil(t, err)
	assert.Equal(t, "go", target)
}

func TestExtractTarGzInvalidGzip(t *testing.T) {
	err := extractTarGz(bytes.NewReader([]byte("not gzip")), t.TempDir())
	assert.NotNil(t, err)
}

func TestExtractTarGzPathTraversal(t *testing.T) {
	tmpDir := t.TempDir()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// Attempt path traversal
	content := []byte("malicious")
	tw.WriteHeader(&tar.Header{Name: "../../../etc/evil", Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len(content))})
	tw.Write(content)
	tw.Close()
	gw.Close()

	err := extractTarGz(bytes.NewReader(buf.Bytes()), tmpDir)
	assert.Nil(t, err) // should not error, just skip

	// File should NOT exist outside tmpDir
	_, err = os.Stat(filepath.Join(tmpDir, "..", "..", "..", "etc", "evil"))
	assert.True(t, os.IsNotExist(err))
}

func TestEnsureGoCachedAlreadyPresent(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a fake cached Go installation
	goRoot := filepath.Join(tmpDir, "go1.99.0")
	goBin := filepath.Join(goRoot, "bin")
	os.MkdirAll(goBin, 0755)
	os.WriteFile(filepath.Join(goBin, "go"), []byte("fake"), 0755)

	// Override goCacheDir to return our tmpDir
	oldCacheDir := goCacheDirFunc
	goCacheDirFunc = func() (string, error) { return tmpDir, nil }
	defer func() { goCacheDirFunc = oldCacheDir }()

	result, err := ensureGoCached("1.99.0")
	assert.Nil(t, err)
	assert.Equal(t, goRoot, result)
}

func TestDownloadGoSuccess(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a tar.gz with go/bin/go inside
	archive := createTestTarGz(t, map[string]string{
		"go/bin/go": "#!/bin/sh\necho go1.99.0",
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	defer srv.Close()

	// Override download URLs
	old := os.Getenv("GOPROXY_FALLBACK")
	os.Unsetenv("GOPROXY_FALLBACK")
	defer os.Setenv("GOPROXY_FALLBACK", old)

	oldURLs := goDownloadURLsFunc
	goDownloadURLsFunc = func(archiveName string) []string {
		return []string{srv.URL + "/" + archiveName}
	}
	defer func() { goDownloadURLsFunc = oldURLs }()

	goRoot := filepath.Join(tmpDir, "go1.99.0")
	err := downloadGo("1.99.0", tmpDir, goRoot)
	assert.Nil(t, err)

	// Verify the binary was extracted and renamed
	content, err := os.ReadFile(filepath.Join(goRoot, "bin", "go"))
	assert.Nil(t, err)
	assert.Equal(t, "#!/bin/sh\necho go1.99.0", string(content))
}

func TestDownloadGoAllFail(t *testing.T) {
	tmpDir := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	oldURLs := goDownloadURLsFunc
	goDownloadURLsFunc = func(archiveName string) []string {
		return []string{srv.URL + "/notfound"}
	}
	defer func() { goDownloadURLsFunc = oldURLs }()

	goRoot := filepath.Join(tmpDir, "go1.99.0")
	err := downloadGo("1.99.0", tmpDir, goRoot)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "all download URLs failed")
}

func TestDownloadGoConnectionError(t *testing.T) {
	tmpDir := t.TempDir()

	oldURLs := goDownloadURLsFunc
	goDownloadURLsFunc = func(archiveName string) []string {
		return []string{"http://127.0.0.1:1/notlistening"}
	}
	defer func() { goDownloadURLsFunc = oldURLs }()

	goRoot := filepath.Join(tmpDir, "go1.99.0")
	err := downloadGo("1.99.0", tmpDir, goRoot)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "all download URLs failed")
}

func TestEnsureGoVersionGoPresent(t *testing.T) {
	// Go is installed in test env and satisfies this project's go.mod,
	// so EnsureGoVersion should succeed without downloading anything.
	err := EnsureGoVersion()
	assert.Nil(t, err)
}

func TestRecordGoMinor(t *testing.T) {
	old := resolvedGoMinor
	defer func() { resolvedGoMinor = old }()

	resolvedGoMinor = 0
	recordGoMinor("1.24.7")
	assert.Equal(t, 24, resolvedGoMinor)

	resolvedGoMinor = 0
	recordGoMinor("1.25.0")
	assert.Equal(t, 25, resolvedGoMinor)

	resolvedGoMinor = 0
	recordGoMinor("1.25")
	assert.Equal(t, 25, resolvedGoMinor)
}
