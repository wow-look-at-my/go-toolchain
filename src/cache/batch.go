package cache

import (
	"archive/tar"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/pierrec/lz4/v4"
)

// Batch flush thresholds.
const (
	// batchSizeThreshold is the maximum body size for an entry to be batched.
	// Entries larger than this are uploaded individually.
	batchSizeThreshold = 64 * 1024 // 64 KB

	// batchFlushBytes triggers a flush when buffered data exceeds this size.
	batchFlushBytes = 1 << 20 // 1 MB

	// batchFlushCount triggers a flush when this many entries are buffered.
	batchFlushCount = 100

	// batchFlushInterval triggers a flush after this duration since the first
	// buffered entry, ensuring data reaches the remote even during slow builds.
	batchFlushInterval = 5 * time.Second
)

// batchManifest is the manifest embedded in each batch archive.
type batchManifest struct {
	Version int                  `json:"version"`
	Created time.Time            `json:"created"`
	Entries []batchManifestEntry `json:"entries"`
}

// batchManifestEntry describes a single cache entry inside a batch.
type batchManifestEntry struct {
	ActionID string `json:"actionID"`
	OutputID string `json:"outputID"`
	Size     int64  `json:"size"`
}

// batchEntry is a pending cache entry waiting to be flushed.
type batchEntry struct {
	actionID string
	outputID string
	data     []byte
}

// batchIndexEntry maps an actionID to a batch file and its metadata.
// Stored in the remote batch index (index.json).
type batchIndexEntry struct {
	Batch    string `json:"batch"`
	OutputID string `json:"outputID"`
	Size     int64  `json:"size"`
}

// createBatch builds a tar archive compressed with LZ4 containing the given
// entries. The archive layout is:
//
//	manifest.json          — index of all entries
//	data/<actionID-1>      — raw file contents
//	data/<actionID-2>
//	...
func createBatch(entries []batchEntry) ([]byte, batchManifest, error) {
	manifest := batchManifest{
		Version: 1,
		Created: time.Now().UTC(),
	}
	for _, e := range entries {
		manifest.Entries = append(manifest.Entries, batchManifestEntry{
			ActionID: e.actionID,
			OutputID: e.outputID,
			Size:     int64(len(e.data)),
		})
	}

	manifestData, err := json.Marshal(manifest)
	if err != nil {
		return nil, batchManifest{}, fmt.Errorf("marshal manifest: %w", err)
	}

	var buf bytes.Buffer
	lz4w := lz4.NewWriter(&buf)
	tw := tar.NewWriter(lz4w)

	// Write manifest first so readers can parse it before the data entries.
	if err := writeTarEntry(tw, "manifest.json", manifestData); err != nil {
		return nil, batchManifest{}, err
	}

	for _, e := range entries {
		if err := writeTarEntry(tw, "data/"+e.actionID, e.data); err != nil {
			return nil, batchManifest{}, err
		}
	}

	if err := tw.Close(); err != nil {
		return nil, batchManifest{}, fmt.Errorf("close tar: %w", err)
	}
	if err := lz4w.Close(); err != nil {
		return nil, batchManifest{}, fmt.Errorf("close lz4: %w", err)
	}

	return buf.Bytes(), manifest, nil
}

// extractFromBatch extracts a single entry from a tar.lz4 batch archive.
// The manifest must appear before the data entries in the archive.
func extractFromBatch(data []byte, actionID string) (outputID string, body []byte, err error) {
	lz4r := lz4.NewReader(bytes.NewReader(data))
	tr := tar.NewReader(lz4r)

	target := "data/" + actionID
	var manifest batchManifest

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", nil, fmt.Errorf("read tar: %w", err)
		}

		if hdr.Name == "manifest.json" {
			raw, err := io.ReadAll(tr)
			if err != nil {
				return "", nil, fmt.Errorf("read manifest: %w", err)
			}
			if err := json.Unmarshal(raw, &manifest); err != nil {
				return "", nil, fmt.Errorf("parse manifest: %w", err)
			}
			continue
		}

		if hdr.Name == target {
			body, err = io.ReadAll(tr)
			if err != nil {
				return "", nil, fmt.Errorf("read entry: %w", err)
			}
			for _, e := range manifest.Entries {
				if e.ActionID == actionID {
					return e.OutputID, body, nil
				}
			}
			return "", nil, fmt.Errorf("entry %s found in tar but not in manifest", actionID)
		}
	}

	return "", nil, fmt.Errorf("entry %s not found in batch", actionID)
}

// extractedEntry holds a single extracted cache entry from a batch.
type extractedEntry struct {
	ActionID string
	OutputID string
	Data     []byte
}

// extractAllFromBatch extracts every entry from a tar.lz4 batch archive.
// Used for proactive local cache population: when one entry triggers a batch
// download, all sibling entries are extracted so subsequent GETs hit locally.
func extractAllFromBatch(data []byte) ([]extractedEntry, error) {
	lz4r := lz4.NewReader(bytes.NewReader(data))
	tr := tar.NewReader(lz4r)

	var manifest batchManifest
	entries := map[string][]byte{} // actionID → data

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}

		if hdr.Name == "manifest.json" {
			raw, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("read manifest: %w", err)
			}
			if err := json.Unmarshal(raw, &manifest); err != nil {
				return nil, fmt.Errorf("parse manifest: %w", err)
			}
			continue
		}

		if len(hdr.Name) > 5 && hdr.Name[:5] == "data/" {
			actionID := hdr.Name[5:]
			body, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("read entry %s: %w", actionID, err)
			}
			entries[actionID] = body
		}
	}

	var result []extractedEntry
	for _, me := range manifest.Entries {
		if data, ok := entries[me.ActionID]; ok {
			result = append(result, extractedEntry{
				ActionID: me.ActionID,
				OutputID: me.OutputID,
				Data:     data,
			})
		}
	}
	return result, nil
}

// readBatchManifest reads only the manifest from a batch archive without
// extracting data entries.
func readBatchManifest(data []byte) (batchManifest, error) {
	lz4r := lz4.NewReader(bytes.NewReader(data))
	tr := tar.NewReader(lz4r)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return batchManifest{}, fmt.Errorf("read tar: %w", err)
		}

		if hdr.Name == "manifest.json" {
			raw, err := io.ReadAll(tr)
			if err != nil {
				return batchManifest{}, fmt.Errorf("read manifest: %w", err)
			}
			var m batchManifest
			if err := json.Unmarshal(raw, &m); err != nil {
				return batchManifest{}, fmt.Errorf("parse manifest: %w", err)
			}
			return m, nil
		}
	}

	return batchManifest{}, fmt.Errorf("manifest not found in batch")
}

func writeTarEntry(tw *tar.Writer, name string, data []byte) error {
	if err := tw.WriteHeader(&tar.Header{
		Name: name,
		Size: int64(len(data)),
		Mode: 0644,
	}); err != nil {
		return fmt.Errorf("write header %s: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("write data %s: %w", name, err)
	}
	return nil
}

// batchName generates a unique name for a batch archive.
// Format: batch-<unix-seconds>-<random-hex>
func batchName() string {
	var buf [8]byte
	rand.Read(buf[:])
	return fmt.Sprintf("batch-%d-%s", time.Now().Unix(), hex.EncodeToString(buf[:]))
}
