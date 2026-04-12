package cache

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// uploadObject PUTs raw data to the given S3 key.
func (b *WebBackend) uploadObject(key string, data []byte) error {
	req, err := http.NewRequest("PUT", b.url(key), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(data))
	b.signRequest(req)

	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}
	return nil
}

// loadBatchIndex fetches the batch index from the remote server.
// Returns an empty map if the index doesn't exist or can't be loaded.
func (b *WebBackend) loadBatchIndex() map[string]batchIndexEntry {
	indexKey := b.prefix + "batches/index-json"
	req, err := http.NewRequest("GET", b.url(indexKey), nil)
	if err != nil {
		return make(map[string]batchIndexEntry)
	}
	b.signRequest(req)

	resp, err := b.client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		return make(map[string]batchIndexEntry)
	}
	defer resp.Body.Close()

	var index map[string]batchIndexEntry
	if err := json.NewDecoder(resp.Body).Decode(&index); err != nil {
		return make(map[string]batchIndexEntry)
	}
	return index
}

// uploadBatchIndex persists the batch index to the remote server.
// It performs a read-modify-write merge so concurrent uploaders don't
// overwrite each other's entries (eventual consistency is acceptable).
func (b *WebBackend) uploadBatchIndex(localIndex map[string]batchIndexEntry) {
	// Download current remote index and merge.
	remoteIndex := b.downloadBatchIndex()
	for k, v := range localIndex {
		remoteIndex[k] = v
	}

	data, err := json.Marshal(remoteIndex)
	if err != nil {
		return
	}

	indexKey := b.prefix + "batches/index-json"
	b.uploadObject(indexKey, data)
}

// downloadBatchIndex fetches the current remote batch index.
func (b *WebBackend) downloadBatchIndex() map[string]batchIndexEntry {
	indexKey := b.prefix + "batches/index-json"
	req, err := http.NewRequest("GET", b.url(indexKey), nil)
	if err != nil {
		return make(map[string]batchIndexEntry)
	}
	b.signRequest(req)

	resp, err := b.client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		return make(map[string]batchIndexEntry)
	}
	defer resp.Body.Close()

	var index map[string]batchIndexEntry
	if err := json.NewDecoder(resp.Body).Decode(&index); err != nil {
		return make(map[string]batchIndexEntry)
	}
	return index
}
