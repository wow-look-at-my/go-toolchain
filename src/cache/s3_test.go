package cache

import (
	"testing"

	"github.com/wow-look-at-my/testify/require"
)

func TestCompressDecompress_RoundTrip(t *testing.T) {
	data := []byte("hello world, this is test data for compression")

	compressed, err := compressData(data)
	require.NoError(t, err)
	require.NotEmpty(t, compressed)

	decompressed, err := decompressData(compressed)
	require.NoError(t, err)
	require.Equal(t, data, decompressed)
}

func TestCompressDecompress_Empty(t *testing.T) {
	compressed, err := compressData([]byte{})
	require.NoError(t, err)

	decompressed, err := decompressData(compressed)
	require.NoError(t, err)
	require.Empty(t, decompressed)
}

func TestCompressDecompress_Large(t *testing.T) {
	// Create repetitive data that compresses well.
	data := make([]byte, 100000)
	for i := range data {
		data[i] = byte(i % 256)
	}

	compressed, err := compressData(data)
	require.NoError(t, err)

	decompressed, err := decompressData(compressed)
	require.NoError(t, err)
	require.Equal(t, data, decompressed)
}

func TestNewS3Backend_EmptyBucket(t *testing.T) {
	b, err := NewS3Backend("", "", "")
	require.NoError(t, err)
	require.Nil(t, b)
}

func TestS3Backend_Key(t *testing.T) {
	b := &S3Backend{prefix: "my-prefix/"}
	require.Equal(t, "my-prefix/v1abcdef", b.key("abcdef"))
}

func TestS3Backend_KeyDefaultPrefix(t *testing.T) {
	b := &S3Backend{prefix: "go-buildcache/"}
	require.Equal(t, "go-buildcache/v1abc123", b.key("abc123"))
}
