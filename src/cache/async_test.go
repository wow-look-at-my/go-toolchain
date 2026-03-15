package cache

import (
	"strings"
	"testing"
	"github.com/wow-look-at-my/testify/require"
)

func TestAsyncBackend_PutAndGet(t *testing.T) {
	inner := newMemBackend()
	async := NewAsyncBackend(inner)

	err := async.Put("action1", "output1", strings.NewReader("data1"), 5)
	require.Nil(t, err)

	// Close waits for pending writes.
	require.NoError(t, async.Close())

	// Verify the data was written to inner.
	outputID, body, _, _, miss, err := inner.Get("action1")
	require.Nil(t, err)

	require.False(t, miss)

	require.Equal(t, "output1", outputID)

	buf := make([]byte, 100)
	n, _ := body.Read(buf)
	require.Equal(t, "data1", string(buf[:n]))

}

func TestAsyncBackend_Get(t *testing.T) {
	inner := newMemBackend()
	inner.Put("action1", "output1", strings.NewReader("data1"), 5)

	async := NewAsyncBackend(inner)
	outputID, _, _, _, miss, err := async.Get("action1")
	require.Nil(t, err)

	require.False(t, miss)

	require.Equal(t, "output1", outputID)

}
