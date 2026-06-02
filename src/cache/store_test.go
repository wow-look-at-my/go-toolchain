package cache

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNewLocalStore_RoundTrip verifies that whichever backend NewLocalStore
// selects — the FUSE virtual filesystem when available, the loose-file cache
// otherwise — honours the core contract: Put returns a DiskPath the toolchain
// can open whose bytes match, and Get round-trips the metadata.
func TestNewLocalStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLocalStore(dir)
	require.Nil(t, err)
	defer store.Close()

	aid, oid := hexID(1), hexID(100)
	body := []byte("backend-agnostic round trip")

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
