package cache

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// populateCacheRoot lays out a realistic pre-purge buildcache root: pack data,
// loose-tier bucket data, a stray root-level temp file, plus the entries a
// purge must never touch (mnt/ with content, the lock file, unknown names).
func populateCacheRoot(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "packs", "pack-000001.data"), []byte("pack bytes"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "ab"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ab", "v1abcdef"), []byte("loose body"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ab", "v1abcdef.meta"), []byte("outputID:ff\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".tmp-stray"), []byte("temp"), 0o644))
	// Must-survive entries.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "mnt"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "mnt", "leftover"), []byte("never touch a mountpoint"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, fuseLockName), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "unknown.bin"), []byte("not ours to delete"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "unknown-dir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "unknown-dir", "keep"), []byte("survives"), 0o644))
}

func requireStampIs(t *testing.T, root string, version int) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, localCacheVersionFile))
	require.NoError(t, err)
	require.Equal(t, strconv.Itoa(version)+"\n", string(raw))
}

// A populated root with no stamp is implicit version 1: the data dirs are
// purged, the stamp is written, and everything that is not cache data —
// mnt/ (a possibly-mounted FUSE mountpoint), the lock file, unknown names —
// survives untouched.
func TestEnsureLocalCacheVersion_PurgesUnstampedRoot(t *testing.T) {
	root := t.TempDir()
	populateCacheRoot(t, root)

	EnsureLocalCacheVersion(root)

	requireStampIs(t, root, currentLocalCacheVersion)
	for _, gone := range []string{"packs", "ab", ".tmp-stray"} {
		_, err := os.Stat(filepath.Join(root, gone))
		require.True(t, os.IsNotExist(err), "%s must be purged", gone)
	}
	for _, kept := range []string{
		filepath.Join("mnt", "leftover"),
		fuseLockName,
		"unknown.bin",
		filepath.Join("unknown-dir", "keep"),
	} {
		_, err := os.Stat(filepath.Join(root, kept))
		require.NoError(t, err, "%s must survive the purge", kept)
	}
}

// An unparseable stamp counts as version 1 and purges too.
func TestEnsureLocalCacheVersion_PurgesUnparseableStamp(t *testing.T) {
	root := t.TempDir()
	populateCacheRoot(t, root)
	require.NoError(t, os.WriteFile(filepath.Join(root, localCacheVersionFile), []byte("garbage"), 0o644))

	EnsureLocalCacheVersion(root)

	requireStampIs(t, root, currentLocalCacheVersion)
	_, err := os.Stat(filepath.Join(root, "packs"))
	require.True(t, os.IsNotExist(err), "an unparseable stamp must be treated as version 1 and purge")
}

// A root already stamped with the current version is left completely alone: a
// sentinel planted inside the packs dir survives.
func TestEnsureLocalCacheVersion_CurrentStampDeletesNothing(t *testing.T) {
	root := t.TempDir()
	populateCacheRoot(t, root)
	sentinel := filepath.Join(root, "packs", "pack-000001.data")
	require.NoError(t, os.WriteFile(filepath.Join(root, localCacheVersionFile),
		[]byte(strconv.Itoa(currentLocalCacheVersion)+"\n"), 0o644))

	EnsureLocalCacheVersion(root)

	data, err := os.ReadFile(sentinel)
	require.NoError(t, err, "a current-version root must not be purged")
	require.Equal(t, "pack bytes", string(data))
	_, err = os.Stat(filepath.Join(root, "ab", "v1abcdef"))
	require.NoError(t, err)
	requireStampIs(t, root, currentLocalCacheVersion)
}

// A fresh, empty root is stamped without error (nothing to purge).
func TestEnsureLocalCacheVersion_FreshRootStampedSilently(t *testing.T) {
	root := t.TempDir()

	EnsureLocalCacheVersion(root)

	requireStampIs(t, root, currentLocalCacheVersion)
}

// A missing root dir is created (the constructors create it moments later
// anyway; the stamp needs it now).
func TestEnsureLocalCacheVersion_CreatesMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not", "yet", "created")

	EnsureLocalCacheVersion(root)

	requireStampIs(t, root, currentLocalCacheVersion)
}

// Idempotence: the run after a purge sees the current stamp and re-stored data
// stays put.
func TestEnsureLocalCacheVersion_SecondRunKeepsNewData(t *testing.T) {
	root := t.TempDir()
	populateCacheRoot(t, root)
	EnsureLocalCacheVersion(root)

	// A post-purge process repopulates the cache...
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "packs", "pack-000001.data"), []byte("new data"), 0o644))

	// ...and later runs must not touch it.
	EnsureLocalCacheVersion(root)
	data, err := os.ReadFile(filepath.Join(root, "packs", "pack-000001.data"))
	require.NoError(t, err)
	require.Equal(t, "new data", string(data))
}

func TestIsLooseBucketName(t *testing.T) {
	require.True(t, isLooseBucketName("00"))
	require.True(t, isLooseBucketName("ab"))
	require.True(t, isLooseBucketName("ff"))
	require.False(t, isLooseBucketName("mnt")) // 3 chars: the FUSE mountpoint
	require.False(t, isLooseBucketName("AB"))  // buckets are lowercase hex
	require.False(t, isLooseBucketName("g0"))
	require.False(t, isLooseBucketName("a"))
	require.False(t, isLooseBucketName(""))
}
