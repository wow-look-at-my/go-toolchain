package cmd

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserCacheRootPrefersAbsoluteXDG(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)

	got, err := userCacheRoot()
	require.NoError(t, err)
	assert.Equal(t, dir, got)
}

// A relative XDG_CACHE_HOME is invalid per the XDG spec, so $HOME/.cache wins.
func TestUserCacheRootIgnoresRelativeXDG(t *testing.T) {
	t.Serial()
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("XDG_CACHE_HOME", "relative/cache")

	got, err := userCacheRoot()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".cache"), got)
}

// The cache path is the same on every host. os.UserCacheDir is what would put
// it under ~/Library/Caches on darwin.
func TestUserCacheRootFallsBackToHomeCache(t *testing.T) {
	t.Serial()
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("XDG_CACHE_HOME", "")

	got, err := userCacheRoot()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".cache"), got)
	assert.NotContains(t, got, "Library")
}

func TestGoCacheDirUnderHomeCache(t *testing.T) {
	t.Serial()
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("XDG_CACHE_HOME", "")

	got, err := goCacheDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".cache", "go-toolchain"), got)
}

func TestOpenDepsCacheUnderHomeCache(t *testing.T) {
	t.Serial()
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("XDG_CACHE_HOME", "")

	c, err := openDepsCache()
	require.NoError(t, err)
	defer c.close()

	fc, ok := c.(*fileDepsCache)
	require.True(t, ok)
	assert.Equal(t, filepath.Join(home, ".cache", cacheSubdir, cacheFile), fc.path)
}
