package cache

import (
	"encoding/json"
	"sync/atomic"
)

// AtomicCounter is a uint32 counter with atomic access and JSON support.
type AtomicCounter struct{ v atomic.Uint32 }

func (c *AtomicCounter) Add(delta uint32) { c.v.Add(delta) }
func (c *AtomicCounter) Increment()       { c.v.Add(1) }
func (c *AtomicCounter) Decrement()       { c.v.Add(^uint32(0)) }
func (c *AtomicCounter) Load() uint32     { return c.v.Load() }
func (c *AtomicCounter) Store(val uint32) { c.v.Store(val) }

func (c *AtomicCounter) MarshalJSON() ([]byte, error) { return json.Marshal(c.v.Load()) }

func (c *AtomicCounter) UnmarshalJSON(data []byte) error {
	var v uint32
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	c.v.Store(v)
	return nil
}

// CacheStats tracks get/put counters for a single cache layer.
type CacheStats struct {
	Hits AtomicCounter `json:"hits"`
	Puts AtomicCounter `json:"puts"`
}
