package test

import (
	"os"
	"testing"
	"github.com/stretchr/testify/require"

)

func TestWatermarkGetSetRoundTrip(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, SetWatermark(dir, 85.3))

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

	require.NoError(t, SetWatermark(dir, 90.0))

	require.NoError(t, RemoveWatermark(dir))

	_, exists, err := GetWatermark(dir)
	require.Nil(t, err)
	require.False(t, exists)
}

func TestWatermarkRemoveWhenNoneExists(t *testing.T) {
	dir := t.TempDir()

	// Should not error when removing a non-existent watermark
	require.NoError(t, RemoveWatermark(dir))
}

func TestWatermarkGetOnFile(t *testing.T) {
	// Verify it works on a file too, not just directories
	f, err := os.CreateTemp(t.TempDir(), "watermark-test")
	require.Nil(t, err)
	f.Close()

	require.NoError(t, SetWatermark(f.Name(), 42.5))

	val, exists, err := GetWatermark(f.Name())
	require.Nil(t, err)
	require.True(t, exists)
	require.Equal(t, 42.5, val)
}
