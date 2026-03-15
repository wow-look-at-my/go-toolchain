package cache

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalCache_PutAndGet(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	if err != nil {
		t.Fatal(err)
	}

	actionID := "aabbccdd00112233aabbccdd00112233"
	outputID := "11223344556677881122334455667788"
	body := []byte("hello, cache")

	diskPath, err := lc.Put(actionID, outputID, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if diskPath == "" {
		t.Fatal("expected non-empty disk path")
	}

	// Verify file content.
	got, err := os.ReadFile(diskPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("content mismatch: got %q, want %q", got, body)
	}

	// Get should return the cached entry.
	meta, miss := lc.Get(actionID)
	if miss {
		t.Fatal("expected cache hit")
	}
	if meta.OutputID != outputID {
		t.Fatalf("outputID mismatch: got %q, want %q", meta.OutputID, outputID)
	}
	if meta.DiskPath != diskPath {
		t.Fatalf("diskPath mismatch: got %q, want %q", meta.DiskPath, diskPath)
	}
	if meta.Size != int64(len(body)) {
		t.Fatalf("size mismatch: got %d, want %d", meta.Size, len(body))
	}
}

func TestLocalCache_Miss(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	if err != nil {
		t.Fatal(err)
	}

	_, miss := lc.Get("deadbeefdeadbeefdeadbeefdeadbeef")
	if !miss {
		t.Fatal("expected cache miss")
	}
}

func TestLocalCache_Overwrite(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	if err != nil {
		t.Fatal(err)
	}

	actionID := "aabbccdd00112233aabbccdd00112233"

	_, err = lc.Put(actionID, "aaaa", bytes.NewReader([]byte("first")))
	if err != nil {
		t.Fatal(err)
	}

	_, err = lc.Put(actionID, "bbbb", bytes.NewReader([]byte("second")))
	if err != nil {
		t.Fatal(err)
	}

	meta, miss := lc.Get(actionID)
	if miss {
		t.Fatal("expected hit")
	}
	if meta.OutputID != "bbbb" {
		t.Fatalf("expected outputID bbbb, got %q", meta.OutputID)
	}
}

func TestLocalCache_SubdirCreation(t *testing.T) {
	dir := t.TempDir()
	_, err := NewLocalCache(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Verify 256 subdirs exist.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 256 {
		t.Fatalf("expected 256 subdirs, got %d", len(entries))
	}
}

func TestLocalCache_DataPath(t *testing.T) {
	lc := &LocalCache{dir: "/tmp/test-cache"}
	path := lc.dataPath("aabbccdd")
	expected := filepath.Join("/tmp/test-cache", "aa", "v1aabbccdd")
	if path != expected {
		t.Fatalf("expected %q, got %q", expected, path)
	}
}

func TestParseMeta(t *testing.T) {
	now := time.Now().Unix()
	raw := "outputID:deadbeef\nsize:42\ntime:" + itoa(now) + "\n"
	m, err := parseMeta(raw)
	if err != nil {
		t.Fatal(err)
	}
	if m.OutputID != "deadbeef" {
		t.Fatalf("outputID: got %q", m.OutputID)
	}
	if m.Size != 42 {
		t.Fatalf("size: got %d", m.Size)
	}
	if m.Time.Unix() != now {
		t.Fatalf("time: got %d, want %d", m.Time.Unix(), now)
	}
}

func TestParseMeta_MissingOutputID(t *testing.T) {
	_, err := parseMeta("size:42\ntime:123\n")
	if err == nil {
		t.Fatal("expected error for missing outputID")
	}
}

func TestParseMeta_InvalidSize(t *testing.T) {
	_, err := parseMeta("outputID:abc\nsize:not-a-number\n")
	if err == nil {
		t.Fatal("expected error for invalid size")
	}
}

func TestParseMeta_InvalidTime(t *testing.T) {
	_, err := parseMeta("outputID:abc\ntime:not-a-number\n")
	if err == nil {
		t.Fatal("expected error for invalid time")
	}
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
