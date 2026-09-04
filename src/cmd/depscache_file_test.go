package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tempDepsCache(t *testing.T) *fileDepsCache {
	t.Helper()
	path := filepath.Join(t.TempDir(), cacheFile)
	return &fileDepsCache{path: path, entries: readDepsCacheFile(path)}
}

// What close writes, a later open reads back -- the whole point of the cache.
func TestFileDepsCacheSurvivesClose(t *testing.T) {
	t.Serial()
	c := tempDepsCache(t)
	c.store("example.com/mod", "v1.2.3", "v1.3.0", 1700000000)
	c.store("example.com/current", "v2.0.0", "", 1700000001)
	c.close()

	reopened := &fileDepsCache{path: c.path, entries: readDepsCacheFile(c.path)}

	update, checkedAt, found := reopened.lookup("example.com/mod", "v1.2.3")
	require.True(t, found)
	assert.Equal(t, "v1.3.0", update)
	assert.Equal(t, int64(1700000000), checkedAt)

	// An empty update is a real cached answer: the version was current.
	update, _, found = reopened.lookup("example.com/current", "v2.0.0")
	require.True(t, found)
	assert.Equal(t, "", update)

	_, _, found = reopened.lookup("example.com/mod", "v9.9.9")
	assert.False(t, found)
}

// A go-toolchain writing beside this must keep its entries: close merges onto
// the file rather than replacing it with this process's view.
func TestFileDepsCacheMergesConcurrentWriter(t *testing.T) {
	t.Serial()
	c := tempDepsCache(t)
	c.store("example.com/mine", "v1.0.0", "", 1700000000)

	other := &fileDepsCache{path: c.path, entries: map[string]depsCacheEntry{}}
	other.store("example.com/theirs", "v1.0.0", "v1.1.0", 1700000002)
	other.close()

	c.close()

	merged := readDepsCacheFile(c.path)
	assert.Len(t, merged, 2)
	assert.Contains(t, merged, depsCacheKey("example.com/mine", "v1.0.0"))
	assert.Contains(t, merged, depsCacheKey("example.com/theirs", "v1.0.0"))
}

// A damaged file is not a build failure: the entries are recomputable, and a
// cache that refuses to open would take the whole run down with it.
func TestFileDepsCacheDamagedFileReadsEmpty(t *testing.T) {
	t.Serial()
	path := filepath.Join(t.TempDir(), cacheFile)
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o644))

	assert.Empty(t, readDepsCacheFile(path))

	c := &fileDepsCache{path: path, entries: readDepsCacheFile(path)}
	c.store("example.com/mod", "v1.0.0", "", 1700000000)
	c.close()

	_, _, found := (&fileDepsCache{path: path, entries: readDepsCacheFile(path)}).lookup("example.com/mod", "v1.0.0")
	assert.True(t, found)
}
