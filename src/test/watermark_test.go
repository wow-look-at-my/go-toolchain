package test

import (
	"os"
	"testing"
)

func TestWatermarkGetSetRoundTrip(t *testing.T) {
	dir := t.TempDir()

	if err := SetWatermark(dir, 85.3); err != nil {
		t.Fatalf("SetWatermark: %v", err)
	}

	val, exists, err := GetWatermark(dir)
	if err != nil {
		t.Fatalf("GetWatermark: %v", err)
	}
	if !exists {
		t.Fatal("expected watermark to exist")
	}
	if val != 85.3 {
		t.Fatalf("expected 85.3, got %v", val)
	}
}

func TestWatermarkGetWhenNoneExists(t *testing.T) {
	dir := t.TempDir()

	val, exists, err := GetWatermark(dir)
	if err != nil {
		t.Fatalf("GetWatermark: %v", err)
	}
	if exists {
		t.Fatal("expected watermark to not exist")
	}
	if val != 0 {
		t.Fatalf("expected 0, got %v", val)
	}
}

func TestWatermarkRemove(t *testing.T) {
	dir := t.TempDir()

	if err := SetWatermark(dir, 90.0); err != nil {
		t.Fatalf("SetWatermark: %v", err)
	}

	if err := RemoveWatermark(dir); err != nil {
		t.Fatalf("RemoveWatermark: %v", err)
	}

	_, exists, err := GetWatermark(dir)
	if err != nil {
		t.Fatalf("GetWatermark after remove: %v", err)
	}
	if exists {
		t.Fatal("expected watermark to not exist after removal")
	}
}

func TestWatermarkRemoveWhenNoneExists(t *testing.T) {
	dir := t.TempDir()

	// Should not error when removing a non-existent watermark
	if err := RemoveWatermark(dir); err != nil {
		t.Fatalf("RemoveWatermark on non-existent: %v", err)
	}
}

func TestWatermarkGetOnFile(t *testing.T) {
	// Verify it works on a file too, not just directories
	f, err := os.CreateTemp(t.TempDir(), "watermark-test")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	f.Close()

	if err := SetWatermark(f.Name(), 42.5); err != nil {
		t.Fatalf("SetWatermark on file: %v", err)
	}

	val, exists, err := GetWatermark(f.Name())
	if err != nil {
		t.Fatalf("GetWatermark on file: %v", err)
	}
	if !exists {
		t.Fatal("expected watermark to exist on file")
	}
	if val != 42.5 {
		t.Fatalf("expected 42.5, got %v", val)
	}
}
