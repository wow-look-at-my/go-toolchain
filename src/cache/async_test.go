package cache

import (
	"strings"
	"testing"
)

func TestAsyncBackend_PutAndGet(t *testing.T) {
	inner := newMemBackend()
	async := NewAsyncBackend(inner)

	err := async.Put("action1", "output1", strings.NewReader("data1"), 5)
	if err != nil {
		t.Fatal(err)
	}

	// Close waits for pending writes.
	if err := async.Close(); err != nil {
		t.Fatal(err)
	}

	// Verify the data was written to inner.
	outputID, body, _, _, miss, err := inner.Get("action1")
	if err != nil {
		t.Fatal(err)
	}
	if miss {
		t.Fatal("expected hit")
	}
	if outputID != "output1" {
		t.Fatalf("outputID: got %q, want %q", outputID, "output1")
	}
	buf := make([]byte, 100)
	n, _ := body.Read(buf)
	if string(buf[:n]) != "data1" {
		t.Fatalf("body: got %q", buf[:n])
	}
}

func TestAsyncBackend_Get(t *testing.T) {
	inner := newMemBackend()
	inner.Put("action1", "output1", strings.NewReader("data1"), 5)

	async := NewAsyncBackend(inner)
	outputID, _, _, _, miss, err := async.Get("action1")
	if err != nil {
		t.Fatal(err)
	}
	if miss {
		t.Fatal("expected hit")
	}
	if outputID != "output1" {
		t.Fatalf("outputID: got %q, want %q", outputID, "output1")
	}
}
