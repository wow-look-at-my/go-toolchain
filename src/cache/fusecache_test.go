//go:build (linux && !cosmo) || darwin

package cache

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// newFuseCacheForTest mounts a FuseCache, skipping the test when FUSE is unavailable (e.g. no /dev/fuse).
func newFuseCacheForTest(t *testing.T) *FuseCache {
	t.Helper()
	fc, err := newFuseCache(t.TempDir())
	if err != nil {
		t.Skipf("FUSE unavailable: %v", err)
	}
	c := fc.(*FuseCache)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestFuseCache_PutReadThroughMount(t *testing.T) {
	t.Parallel()
	c := newFuseCacheForTest(t)
	body := []byte("served virtually by fuse")
	aid, oid := hexID(1), casID(body)

	diskPath, err := c.Put(aid, oid, bytes.NewReader(body))
	require.Nil(t, err)
	require.Equal(t, filepath.Join(c.mnt, oid), diskPath)

	// Read it back through the kernel — this is exactly the compiler's path.
	got, err := os.ReadFile(diskPath)
	require.Nil(t, err)
	require.Equal(t, body, got)

	// Stat must report the right size; the go command verifies it on a hit.
	info, err := os.Stat(diskPath)
	require.Nil(t, err)
	require.Equal(t, int64(len(body)), info.Size())
}

func TestFuseCache_GetReturnsReadableDiskPath(t *testing.T) {
	t.Parallel()
	c := newFuseCacheForTest(t)
	body := bytes.Repeat([]byte("payload"), 500) // larger than a single read buffer
	aid, oid := hexID(2), casID(body)
	_, err := c.Put(aid, oid, bytes.NewReader(body))
	require.Nil(t, err)

	meta, miss := c.Get(aid)
	require.False(t, miss)
	require.Equal(t, oid, meta.OutputID)
	require.Equal(t, int64(len(body)), meta.Size)
	require.Equal(t, filepath.Join(c.mnt, oid), meta.DiskPath)

	data, err := os.ReadFile(meta.DiskPath)
	require.Nil(t, err)
	require.Equal(t, body, data)
}

// TestFuseCache_CorruptBodyNotServedThroughMount is the regression for the
// serve-path integrity gap. GetVerified guards the GET *RPC* path, but the
// compiler does not read bytes over that RPC: it opens the DiskPath and reads
// the body through the mount (Lookup -> Read). That path must ALSO refuse a
// corrupt body, or the integrity guard is bypassed for exactly the bytes that
// reach the compiler. A body byte rotted in place (header length + recorded
// CRC left intact, so the startup scan still indexes it) must not be served —
// otherwise it surfaces in the go command as "unexpected EOF"/"corrupt index".
//
// Rot is applied across an unmount/remount: within a process a just-Put or
// already-verified record is memoized (records are physically immutable; see
// verify.go), which is exactly how rot manifests in reality — across runs.
func TestFuseCache_CorruptBodyNotServedThroughMount(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fc, err := newFuseCache(dir)
	if err != nil {
		t.Skipf("FUSE unavailable: %v", err)
	}
	c := fc.(*FuseCache)
	body := []byte("export data the compiler must never read corrupted")
	aid, oid := hexID(5), casID(body)
	_, err = c.Put(aid, oid, bytes.NewReader(body))
	require.Nil(t, err)
	loc, ok := c.store.GetByOutput(oid)
	require.True(t, ok)
	require.Nil(t, c.Close()) // unmount; releases the pack handles

	// Rot a body byte, leaving header/CRC intact so the remount scan cannot catch it.
	f, err := os.OpenFile(filepath.Join(dir, "packs", "pack-000001.data"), os.O_RDWR, 0o644)
	require.Nil(t, err)
	var b [1]byte
	_, err = f.ReadAt(b[:], loc.dataOff)
	require.Nil(t, err)
	_, err = f.WriteAt([]byte{b[0] ^ 0xff}, loc.dataOff)
	require.Nil(t, err)
	require.Nil(t, f.Close())

	fc2, err := newFuseCache(dir)
	require.Nil(t, err)
	c2 := fc2.(*FuseCache)
	defer c2.Close()

	// The compiler's exact read path must refuse corrupt bytes (ENOENT), not serve a damaged object.
	got, err := os.ReadFile(filepath.Join(c2.mnt, oid))
	require.NotNil(t, err, "corrupt body must not be served through the mount; got %d bytes back", len(got))
}

func TestFuseCache_Miss(t *testing.T) {
	t.Parallel()
	c := newFuseCacheForTest(t)
	_, miss := c.Get(hexID(7))
	require.True(t, miss)
	// A non-existent body is ENOENT through the mount.
	_, err := os.ReadFile(filepath.Join(c.mnt, hexID(7)))
	require.NotNil(t, err)
}

func TestFuseCache_PersistsAcrossRemount(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fc, err := newFuseCache(dir)
	if err != nil {
		t.Skipf("FUSE unavailable: %v", err)
	}
	c := fc.(*FuseCache)
	body := []byte("persist me across an unmount/remount")
	aid, oid := hexID(3), casID(body)
	_, err = c.Put(aid, oid, bytes.NewReader(body))
	require.Nil(t, err)
	require.Nil(t, c.Close()) // unmount

	fc2, err := newFuseCache(dir)
	require.Nil(t, err)
	c2 := fc2.(*FuseCache)
	defer c2.Close()

	meta, miss := c2.Get(aid)
	require.False(t, miss)
	data, err := os.ReadFile(meta.DiskPath)
	require.Nil(t, err)
	require.Equal(t, body, data)
}

func TestFuseCache_WriteRejected(t *testing.T) {
	t.Parallel()
	c := newFuseCacheForTest(t)
	body := []byte("read only")
	oid := casID(body)
	_, err := c.Put(hexID(4), oid, bytes.NewReader(body))
	require.Nil(t, err)

	// The mount is read-only: opening for write must fail (EROFS as root, EACCES otherwise).
	f, err := os.OpenFile(filepath.Join(c.mnt, oid), os.O_WRONLY, 0)
	if err == nil {
		f.Close()
	}
	require.NotNil(t, err)
}

// A subprocess (the compiler/linker) must be able to read a DiskPath through
// the mount — the whole point of the virtual filesystem.
func TestFuseCache_SubprocessRead(t *testing.T) {
	t.Parallel()
	c := newFuseCacheForTest(t)
	body := []byte("a subprocess must be able to read this")
	dp, err := c.Put(hexID(1), casID(body), bytes.NewReader(body))
	require.Nil(t, err)
	out, err := exec.Command("cat", dp).CombinedOutput()
	require.Nil(t, err, "cat output: %s", out)
	require.Equal(t, body, out)
}

// Many puts then concurrent reads — mimics a parallel build populating and
// reading the cache together.
func TestFuseCache_ConcurrentPutRead(t *testing.T) {
	t.Parallel()
	c := newFuseCacheForTest(t)
	const n = 200
	for i := 0; i < n; i++ {
		body := []byte{byte(i), byte(i)}
		_, err := c.Put(hexID(byte(i)), casID(body), bytes.NewReader(body))
		require.Nil(t, err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var fails int
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			meta, miss := c.Get(hexID(byte(i)))
			if miss {
				return
			}
			if _, err := os.ReadFile(meta.DiskPath); err != nil {
				mu.Lock()
				fails++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	require.Equal(t, 0, fails)
}

// Regression for the mid-build unmount bug: a rival owner on the same cache
// dir must NOT be able to take over (which would unmount the live mount). It
// must fail, leaving the standing owner's mount intact and serving.
func TestFuseCache_SecondOwnerRejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fc, err := newFuseCache(dir)
	if err != nil {
		t.Skipf("FUSE unavailable: %v", err)
	}
	c := fc.(*FuseCache)
	defer c.Close()

	ownerBody := []byte("owned by first")
	_, err = c.Put(hexID(1), casID(ownerBody), bytes.NewReader(ownerBody))
	require.Nil(t, err)

	// A rival newFuseCache on the same dir must be rejected, not clobber.
	_, err2 := newFuseCache(dir)
	require.NotNil(t, err2, "second owner must be rejected to protect the live mount")

	// The standing owner's mount must still serve reads.
	meta, miss := c.Get(hexID(1))
	require.False(t, miss)
	data, err := os.ReadFile(meta.DiskPath)
	require.Nil(t, err)
	require.Equal(t, []byte("owned by first"), data)
}

// NewLocalStore on a dir already owned by a FuseCache must fall back to the
// loose-file cache rather than disturbing the live mount. GOCACHE_FUSE=1 is
// what makes this a real assertion: without it the loose tier is the default
// and the test would pass without ever reaching the busy check.
func TestNewLocalStore_FallsBackWhenBusy(t *testing.T) {
	t.Setenv("GOCACHE_FUSE", "1")
	dir := t.TempDir()
	fc, err := newFuseCache(dir)
	if err != nil {
		t.Skipf("FUSE unavailable: %v", err)
	}
	c := fc.(*FuseCache)
	defer c.Close()

	store, err := NewLocalStore(dir)
	require.Nil(t, err)
	defer store.Close()
	_, isLoose := store.(*LocalCache)
	require.True(t, isLoose, "expected loose-file fallback when the FUSE dir is busy")

	// The live FUSE mount must be unaffected.
	stillBody := []byte("still here")
	_, err = c.Put(hexID(2), casID(stillBody), bytes.NewReader(stillBody))
	require.Nil(t, err)
	m, miss := c.Get(hexID(2))
	require.False(t, miss)
	data, err := os.ReadFile(m.DiskPath)
	require.Nil(t, err)
	require.Equal(t, stillBody, data)
}
