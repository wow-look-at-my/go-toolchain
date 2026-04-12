package cache

import (
	"testing"

	"github.com/wow-look-at-my/testify/require"
)

func TestCreateBatch_RoundTrip(t *testing.T) {
	entries := []batchEntry{
		{actionID: "aabbccdd00112233", outputID: "11223344aabbccdd", data: []byte("hello world")},
		{actionID: "eeff001122334455", outputID: "5566778899aabbcc", data: []byte("second entry")},
	}

	data, manifest, err := createBatch(entries)
	require.NoError(t, err)
	require.NotEmpty(t, data)
	require.Equal(t, 1, manifest.Version)
	require.Len(t, manifest.Entries, 2)

	// Extract first entry.
	outputID, body, err := extractFromBatch(data, "aabbccdd00112233")
	require.NoError(t, err)
	require.Equal(t, "11223344aabbccdd", outputID)
	require.Equal(t, "hello world", string(body))

	// Extract second entry.
	outputID2, body2, err := extractFromBatch(data, "eeff001122334455")
	require.NoError(t, err)
	require.Equal(t, "5566778899aabbcc", outputID2)
	require.Equal(t, "second entry", string(body2))
}

func TestCreateBatch_SingleEntry(t *testing.T) {
	entries := []batchEntry{
		{actionID: "aaaa111122223333", outputID: "bbbb444455556666", data: []byte("only one")},
	}

	data, manifest, err := createBatch(entries)
	require.NoError(t, err)
	require.Len(t, manifest.Entries, 1)
	require.Equal(t, "aaaa111122223333", manifest.Entries[0].ActionID)
	require.Equal(t, int64(8), manifest.Entries[0].Size)

	outputID, body, err := extractFromBatch(data, "aaaa111122223333")
	require.NoError(t, err)
	require.Equal(t, "bbbb444455556666", outputID)
	require.Equal(t, "only one", string(body))
}

func TestCreateBatch_EmptyBody(t *testing.T) {
	entries := []batchEntry{
		{actionID: "abcd000011112222", outputID: "efgh333344445555", data: []byte{}},
	}

	data, _, err := createBatch(entries)
	require.NoError(t, err)

	outputID, body, err := extractFromBatch(data, "abcd000011112222")
	require.NoError(t, err)
	require.Equal(t, "efgh333344445555", outputID)
	require.Empty(t, body)
}

func TestExtractFromBatch_NotFound(t *testing.T) {
	entries := []batchEntry{
		{actionID: "aabbccdd00112233", outputID: "11223344aabbccdd", data: []byte("data")},
	}

	data, _, err := createBatch(entries)
	require.NoError(t, err)

	_, _, err = extractFromBatch(data, "deadbeef00000000")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestReadBatchManifest(t *testing.T) {
	entries := []batchEntry{
		{actionID: "1111222233334444", outputID: "5555666677778888", data: []byte("first")},
		{actionID: "aaaa0000bbbb1111", outputID: "cccc2222dddd3333", data: []byte("second")},
		{actionID: "ffff9999eeee8888", outputID: "dddd7777cccc6666", data: []byte("third")},
	}

	data, _, err := createBatch(entries)
	require.NoError(t, err)

	manifest, err := readBatchManifest(data)
	require.NoError(t, err)
	require.Equal(t, 1, manifest.Version)
	require.Len(t, manifest.Entries, 3)
	require.Equal(t, "1111222233334444", manifest.Entries[0].ActionID)
	require.Equal(t, int64(5), manifest.Entries[0].Size)
	require.Equal(t, "aaaa0000bbbb1111", manifest.Entries[1].ActionID)
	require.Equal(t, "ffff9999eeee8888", manifest.Entries[2].ActionID)
}

func TestReadBatchManifest_Invalid(t *testing.T) {
	_, err := readBatchManifest([]byte("not a valid archive"))
	require.Error(t, err)
}

func TestBatchName_Unique(t *testing.T) {
	names := make(map[string]bool)
	for i := 0; i < 100; i++ {
		n := batchName()
		require.False(t, names[n], "duplicate batch name: %s", n)
		names[n] = true
	}
}

func TestCreateBatch_LargePayload(t *testing.T) {
	// Create entries that together exceed 1MB.
	var entries []batchEntry
	for i := 0; i < 50; i++ {
		data := make([]byte, 30000)
		for j := range data {
			data[j] = byte((i + j) % 256)
		}
		entries = append(entries, batchEntry{
			actionID: "action" + itoa(int64(i)),
			outputID: "output" + itoa(int64(i)),
			data:     data,
		})
	}

	archive, manifest, err := createBatch(entries)
	require.NoError(t, err)
	require.Len(t, manifest.Entries, 50)

	// Verify a few entries round-trip.
	for _, idx := range []int{0, 25, 49} {
		actionID := "action" + itoa(int64(idx))
		outputID, body, err := extractFromBatch(archive, actionID)
		require.NoError(t, err)
		require.Equal(t, "output"+itoa(int64(idx)), outputID)
		require.Len(t, body, 30000)
	}

	// Verify compression saved space (repeated patterns compress well).
	require.Less(t, len(archive), 50*30000, "archive should be smaller than raw data")
}
