package cache

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// Both tiers owe the same contract: Put returns a DiskPath whose bytes match,
// and Get round-trips the metadata.
func TestNewLocalStore_RoundTrip(t *testing.T) {
	for _, tc := range []struct{ name, fuse string }{
		{"default", ""},
		{"packed", "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GOCACHE_FUSE", tc.fuse)
			storeRoundTrip(t)
		})
	}
}

func storeRoundTrip(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewLocalStore(dir)
	require.Nil(t, err)
	defer store.Close()

	body := []byte("backend-agnostic round trip")
	aid, oid := hexID(1), casID(body)

	diskPath, err := store.Put(aid, oid, bytes.NewReader(body))
	require.Nil(t, err)

	got, err := os.ReadFile(diskPath)
	require.Nil(t, err)
	require.Equal(t, body, got)

	meta, miss := store.Get(aid)
	require.False(t, miss)
	require.Equal(t, oid, meta.OutputID)
	require.Equal(t, int64(len(body)), meta.Size)

	data, err := os.ReadFile(meta.DiskPath)
	require.Nil(t, err)
	require.Equal(t, body, data)

	require.NotNil(t, store.StatsPtr())
}

// The default tier must be the one EVERY binary can open: go-fuse is compiled
// out under cosmo, so a packed default splits the cache between flavors. The
// host running this test CAN mount, which is exactly why it needs a guard.
func TestNewLocalStore_DefaultTierIsPortable(t *testing.T) {
	t.Setenv("GOCACHE_FUSE", "")
	store, err := NewLocalStore(t.TempDir())
	require.Nil(t, err)
	defer store.Close()

	_, loose := store.(*LocalCache)
	require.True(t, loose, "the default tier must be the loose-file cache, which a cosmo binary can open")
}
