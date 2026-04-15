package cache

import (
	"archive/tar"
	"encoding/json"
	"fmt"
	"io"
)

// batchGetRequest is the JSON body sent to the server's /_batch/get endpoint.
type batchGetRequest struct {
	Keys     []string `json:"keys"`
	Prefetch bool     `json:"prefetch"`
}

// batchGetManifest is the manifest entry in the server's tar response.
type batchGetManifest struct {
	Entries []batchGetManifestEntry `json:"entries"`
}

type batchGetManifestEntry struct {
	Key      string            `json:"key"`
	Size     int64             `json:"size"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Prefetch bool              `json:"prefetch,omitempty"`
}

// BatchEntry holds a single cache entry from a batch GET response.
type BatchEntry struct {
	Key      string
	OutputID string
	Data     []byte
	Prefetch bool
}

// parseBatchResponse reads a tar stream from the server's /_batch/get
// endpoint and returns all entries with their data and metadata.
func parseBatchResponse(r io.Reader) ([]BatchEntry, error) {
	tr := tar.NewReader(r)

	var manifest batchGetManifest
	dataByKey := map[string][]byte{}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}

		raw, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read entry %s: %w", hdr.Name, err)
		}

		if hdr.Name == "manifest.json" {
			if err := json.Unmarshal(raw, &manifest); err != nil {
				return nil, fmt.Errorf("parse manifest: %w", err)
			}
			continue
		}

		if len(hdr.Name) > 5 && hdr.Name[:5] == "data/" {
			dataByKey[hdr.Name[5:]] = raw
		}
	}

	var entries []BatchEntry
	for _, me := range manifest.Entries {
		data, ok := dataByKey[me.Key]
		if !ok {
			continue
		}
		entries = append(entries, BatchEntry{
			Key:      me.Key,
			OutputID: me.Metadata["outputid"],
			Data:     data,
			Prefetch: me.Prefetch,
		})
	}
	return entries, nil
}
