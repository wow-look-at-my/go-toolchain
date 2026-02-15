package test

import (
	"os"
	"testing"
	"github.com/stretchr/testify/require"

)

func TestWatermarkGetSetRoundTrip(t *testing.T) {
	dir := t.TempDir()

	if err := SetWatermark(dir, 85.3); err != nil {
		t.Fatalf("SetWatermark: %v", err)
	}

	val, exists, err := GetWatermark(dir)
	require.Nil(t, err)
	require.True(t, exists)
	require.Equal(t, 85.3, val)
}

func TestWatermarkGetWhenNoneExists(t *testing.T) {
	dir := t.TempDir()

	val, exists, err := GetWatermark(dir)
	require.Nil(t, err)
	require.False(t, exists)
	require.Equal(t, 0, val)
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
	require.Nil(t, err)
	require.False(t, exists)
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
	require.Nil(t, err)
	f.Close()

	if err := SetWatermark(f.Name(), 42.5); err != nil {
		t.Fatalf("SetWatermark on file: %v", err)
	}

	val, exists, err := GetWatermark(f.Name())
	require.Nil(t, err)
	require.True(t, exists)
	require.Equal(t, 42.5, val)
}
