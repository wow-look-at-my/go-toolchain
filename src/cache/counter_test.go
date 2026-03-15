package cache

import (
	"encoding/json"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestAtomicCounter_IncrementDecrement(t *testing.T) {
	var c AtomicCounter
	c.Increment()
	c.Increment()
	c.Increment()
	assert.Equal(t, uint32(3), c.Load())
	c.Decrement()
	assert.Equal(t, uint32(2), c.Load())
}

func TestAtomicCounter_AddStore(t *testing.T) {
	var c AtomicCounter
	c.Add(10)
	assert.Equal(t, uint32(10), c.Load())
	c.Store(42)
	assert.Equal(t, uint32(42), c.Load())
}

func TestAtomicCounter_JSON(t *testing.T) {
	var c AtomicCounter
	c.Store(123)

	data, err := json.Marshal(&c)
	require.NoError(t, err)
	assert.Equal(t, "123", string(data))

	var c2 AtomicCounter
	require.NoError(t, json.Unmarshal(data, &c2))
	assert.Equal(t, uint32(123), c2.Load())
}

func TestCacheStats_JSON(t *testing.T) {
	var s CacheStats
	s.Hits.Store(10)
	s.Puts.Store(5)

	data, err := json.Marshal(&s)
	require.NoError(t, err)

	var s2 CacheStats
	require.NoError(t, json.Unmarshal(data, &s2))
	assert.Equal(t, uint32(10), s2.Hits.Load())
	assert.Equal(t, uint32(5), s2.Puts.Load())
}
