package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
)

func TestGoVersionLessThan(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"1.24.7", "1.24.11", true},
		{"1.24.11", "1.24.7", false},
		{"1.24.7", "1.24.7", false},
		{"1.23.0", "1.24.0", true},
		{"1.24.0", "1.23.0", false},
		{"1.24", "1.24.1", true},
		{"1.25", "1.24.11", false},
	}
	for _, tt := range tests {
		got := goVersionLessThan(tt.a, tt.b)
		assert.Equal(t, tt.want, got, "%s < %s", tt.a, tt.b)
	}
}

func TestParseVersion(t *testing.T) {
	assert.Equal(t, [3]int{1, 24, 7}, parseVersion("1.24.7"))
	assert.Equal(t, [3]int{1, 24, 11}, parseVersion("go1.24.11"))
	assert.Equal(t, [3]int{1, 25, 0}, parseVersion("1.25"))
}

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

func TestRequiredGoVersionNoMod(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	v, err := requiredGoVersion()
	assert.NotNil(t, err)
	assert.Equal(t, "", v)
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

func TestInstalledGoVersion(t *testing.T) {
	v := installedGoVersion()
	// Should return something like "1.24.7" — at minimum non-empty
	assert.NotEqual(t, "", v)
	assert.Contains(t, v, "1.")
}
