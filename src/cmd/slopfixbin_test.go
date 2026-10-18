package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Not parallel: the resolution reads package-level seams.

// slopfixSeams points every external answer the resolution takes at the test:
// the host it builds for, the cache root, and the download itself. It puts
// the originals back afterwards. download returns stopDownload, so the
// resolution is observed at the point it chooses a URL and a path rather than
// after a network call.
func slopfixSeams(t *testing.T, cacheDir, goos, goarch string) func() *[]downloadAttempt {
	t.Helper()

	oldPlatform := slopfixHostPlatformFunc
	oldDownload := downloadSlopfixFunc
	oldCache := goCacheDirFunc
	t.Cleanup(func() {
		slopfixHostPlatformFunc = oldPlatform
		downloadSlopfixFunc = oldDownload
		goCacheDirFunc = oldCache
	})

	slopfixHostPlatformFunc = func() (string, string) { return goos, goarch }
	goCacheDirFunc = func() (string, error) { return cacheDir, nil }

	attempts := &[]downloadAttempt{}
	downloadSlopfixFunc = func(dlURL, dir, bin string) error {
		*attempts = append(*attempts, downloadAttempt{url: dlURL, dir: dir, bin: bin})
		return errStopDownload
	}
	return func() *[]downloadAttempt { return attempts }
}

type downloadAttempt struct {
	url, dir, bin string
}

// errStopDownload stands in for a download the test refuses to make. The
// resolution must carry it back, so a wrong URL or path is reported rather
// than swallowed.
var errStopDownload = errors.New("download stopped by the test")

// The pin in GO_TOOLCHAIN_SLOPFIX_VERSION decides which buildhost slot the
// resolution asks for, and an empty pin must not collapse onto the pinned
// directory: a pinned run and an unpinned one cache separately, so a new
// release never serves a stale binary to the other.
func TestEnsureSlopfixChoosesTheURLAndDirectoryForThePin(t *testing.T) {
	for _, tc := range []struct {
		name     string
		pin      string
		wantURL  string
		wantDir  string
		wantFile string
	}{
		{
			name:    "pinned",
			pin:     "944",
			wantURL: "https://dl.pazer.build/slopfix?v=944&os=linux&arch=amd64",
		},
		{
			name:    "unpinned",
			pin:     "",
			wantURL: "https://dl.pazer.build/slopfix?os=linux&arch=amd64",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := t.TempDir()
			// The empty string reads as unset through os.Getenv, which is how
			// the resolution tells a pin from no pin.
			t.Setenv(slopfixVersionEnv, tc.pin)
			t.Setenv(slopfixBinEnv, "")
			attempts := slopfixSeams(t, cache, "linux", "amd64")

			_, err := ensureSlopfix()
			require.ErrorIs(t, err, errStopDownload, "the download error must reach the caller")

			got := *attempts()
			require.Len(t, got, 1, "exactly one download is attempted")
			assert.Equal(t, tc.wantURL, got[0].url)

			key := "latest"
			if tc.pin != "" {
				key = tc.pin
			}
			dir := filepath.Join(cache, "slopfix", key)
			assert.Equal(t, dir, got[0].dir)
			assert.Equal(t, filepath.Join(dir, "slopfix"), got[0].bin)
		})
	}
}

// A cached binary is the whole point of the cache: the resolution returns it
// and asks buildhost for nothing.
func TestEnsureSlopfixReturnsTheCachedBinaryWithoutDownloading(t *testing.T) {
	cache := t.TempDir()
	t.Setenv(slopfixVersionEnv, "944")
	t.Setenv(slopfixBinEnv, "")
	attempts := slopfixSeams(t, cache, "linux", "amd64")

	bin := filepath.Join(cache, "slopfix", "944", "slopfix")
	require.NoError(t, os.MkdirAll(filepath.Dir(bin), 0755))
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\n"), 0755))

	got, err := ensureSlopfix()
	require.NoError(t, err)
	assert.Equal(t, bin, got)
	assert.Empty(t, *attempts(), "a warm cache must not download")
}

// NT names its executables, and a fat APE reports "cosmo" as runtime.GOOS, so
// the cached name comes from the host OS rather than the compile-time one.
func TestEnsureSlopfixNamesTheBinaryForTheHostPlatform(t *testing.T) {
	cache := t.TempDir()
	t.Setenv(slopfixVersionEnv, "944")
	t.Setenv(slopfixBinEnv, "")
	attempts := slopfixSeams(t, cache, "windows", "amd64")

	_, err := ensureSlopfix()
	require.ErrorIs(t, err, errStopDownload)

	got := *attempts()
	require.Len(t, got, 1)
	assert.Equal(t, filepath.Join(cache, "slopfix", "944", "slopfix.exe"), got[0].bin)
	assert.Equal(t, "https://dl.pazer.build/slopfix?v=944&os=windows&arch=amd64", got[0].url)
}

// The pin arrives from the environment and becomes one directory name. A
// separator or a parent reference in it must not be able to walk out of the
// cache root.
func TestSlopfixCacheKeyKeepsThePinInsideOneSegment(t *testing.T) {
	for _, tc := range []struct{ pin, want string }{
		{"", "latest"},
		{"944", "944"},
		{"v1.2.3", "v1.2.3"},
		{"v1_2-3", "v1_2-3"},
		{"../../etc", "..-..-etc"},
		{"a/b", "a-b"},
		{"a b\nb", "a-b-b"},
	} {
		assert.Equal(t, tc.want, slopfixCacheKey(tc.pin), "pin %q", tc.pin)
	}
}
