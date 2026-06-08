package cache

import (
	"bytes"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalCache_PutAndGet(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.Nil(t, err)

	actionID := "aabbccdd00112233aabbccdd00112233"
	outputID := "11223344556677881122334455667788"
	body := []byte("hello, cache")

	diskPath, err := lc.Put(actionID, outputID, bytes.NewReader(body))
	require.Nil(t, err)

	require.NotEqual(t, "", diskPath)

	// Verify file content.
	got, err := os.ReadFile(diskPath)
	require.Nil(t, err)

	require.True(t, bytes.Equal(got, body))

	// Get should return the cached entry.
	meta, miss := lc.Get(actionID)
	require.False(t, miss)

	require.Equal(t, outputID, meta.OutputID)

	require.Equal(t, diskPath, meta.DiskPath)

	require.Equal(t, int64(len(body)), meta.Size)

}

func TestLocalCache_Miss(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.Nil(t, err)

	_, miss := lc.Get("deadbeefdeadbeefdeadbeefdeadbeef")
	require.True(t, miss)

}

func TestLocalCache_Overwrite(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.Nil(t, err)

	actionID := "aabbccdd00112233aabbccdd00112233"

	_, err = lc.Put(actionID, "aaaa", bytes.NewReader([]byte("first")))
	require.Nil(t, err)

	_, err = lc.Put(actionID, "bbbb", bytes.NewReader([]byte("second")))
	require.Nil(t, err)

	meta, miss := lc.Get(actionID)
	require.False(t, miss)

	require.Equal(t, "bbbb", meta.OutputID)

}

func TestLocalCache_SubdirCreation(t *testing.T) {
	dir := t.TempDir()
	_, err := NewLocalCache(dir)
	require.Nil(t, err)

	// Verify 256 subdirs exist.
	entries, _ := os.ReadDir(dir)
	require.Equal(t, 256, len(entries))

}

func TestLocalCache_DataPath(t *testing.T) {
	lc := &LocalCache{dir: "/tmp/test-cache"}
	path := lc.dataPath("aabbccdd")
	expected := filepath.Join("/tmp/test-cache", "aa", "v1aabbccdd")
	require.Equal(t, expected, path)

}

func TestParseMeta(t *testing.T) {
	now := time.Now().Unix()
	raw := "outputID:deadbeef\nsize:42\ntime:" + itoa(now) + "\n"
	m, err := parseMeta(raw)
	require.Nil(t, err)

	require.Equal(t, "deadbeef", m.OutputID)

	require.Equal(t, int64(42), m.Size)

	require.Equal(t, now, m.Time.Unix())

}

func TestParseMeta_MissingOutputID(t *testing.T) {
	_, err := parseMeta("size:42\ntime:123\n")
	require.NotNil(t, err)

}

func TestParseMeta_InvalidSize(t *testing.T) {
	_, err := parseMeta("outputID:abc\nsize:not-a-number\n")
	require.NotNil(t, err)

}

func TestParseMeta_InvalidTime(t *testing.T) {
	_, err := parseMeta("outputID:abc\ntime:not-a-number\n")
	require.NotNil(t, err)

}

func itoa(n int64) string {
	return string([]byte{
		byte('0' + (n/1000000000)%10),
		byte('0' + (n/100000000)%10),
		byte('0' + (n/10000000)%10),
		byte('0' + (n/1000000)%10),
		byte('0' + (n/100000)%10),
		byte('0' + (n/10000)%10),
		byte('0' + (n/1000)%10),
		byte('0' + (n/100)%10),
		byte('0' + (n/10)%10),
		byte('0' + n%10),
	})
}
