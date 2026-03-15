package cache

import (
	"bytes"
	"io"
	"sync"
	"time"
)

// AsyncBackend wraps a Backend so that Put calls are non-blocking.
// Get calls are forwarded synchronously.
type AsyncBackend struct {
	inner Backend
	wg    sync.WaitGroup
}

// NewAsyncBackend wraps inner so Put operations run in goroutines.
func NewAsyncBackend(inner Backend) *AsyncBackend {
	return &AsyncBackend{inner: inner}
}

func (a *AsyncBackend) Get(actionID string) (string, io.ReadCloser, int64, time.Time, bool, error) {
	return a.inner.Get(actionID)
}

func (a *AsyncBackend) Put(actionID, outputID string, body io.Reader, bodySize int64) error {
	// Read body now since the caller may reuse the buffer.
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.inner.Put(actionID, outputID, bytes.NewReader(data), int64(len(data)))
	}()
	return nil
}

func (a *AsyncBackend) Close() error {
	a.wg.Wait()
	return a.inner.Close()
}
