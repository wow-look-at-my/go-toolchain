package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
)

// gbciKeyPrefix is the constant leading portion of every cacheprog cache
// key. The wire format ships only the variable 32-byte action-ID hash that
// follows this prefix; both client and server hardcode the same prefix.
const gbciKeyPrefix = "go-buildcache/v1"

// gbciHashSize is the number of bytes per entry in the index body.
const gbciHashSize = 32

// gbciHeaderSize is the fixed header size in bytes.
const gbciHeaderSize = 24

// gbciVersion is the wire-format version stored in the header.
const gbciVersion = 1

// gbciMagic is the four-byte file-format identifier "GBCI".
var gbciMagic = [4]byte{'G', 'B', 'C', 'I'}

// indexCachePath returns the path for the local on-disk index blob.
// The hash makes the path unique per (endpoint, bucket, prefix) so multiple
// daemons on the same machine targeting different caches don't collide.
func (b *WebBackend) indexCachePath() string {
	h := sha256.Sum256([]byte(b.endpoint + "/" + b.bucket + "/" + b.prefix))
	name := "gocache-web-index-" + hex.EncodeToString(h[:8]) + ".bin"
	return filepath.Join(os.TempDir(), name)
}

// loadOrFetchIndex returns the set of known cache keys for this backend.
// It reads any previously cached blob from disk, then issues a conditional
// GET /<bucket>/_index against the server. On 304 we keep the disk blob;
// on 200 we adopt the new one and persist it. Any failure produces an
// empty set — Get/Put still work, they just always miss b.keys until the
// next refresh.
func (b *WebBackend) loadOrFetchIndex() set.Set[string] {
	path := b.indexCachePath()
	diskBlob, diskKeys, diskETag := b.readDiskIndex(path)

	blob, status, err := b.fetchIndexBlob(diskETag)
	if err != nil {
		b.noteRemoteResult(true)
		fmt.Fprintf(os.Stderr, "cacheprog: web index fetch: %v\n", err)
		if diskBlob != nil {
			return diskKeys
		}
		return set.New[string]()
	}
	b.noteRemoteResult(false)
	if status == http.StatusNotModified {
		if diskBlob != nil {
			return diskKeys
		}
		// Server claimed not-modified but we have no disk copy (likely a
		// cleared /tmp between the ETag fetch and now). Refetch unconditionally.
		blob, _, err = b.fetchIndexBlob("")
		if err != nil {
			fmt.Fprintf(os.Stderr, "cacheprog: web index refetch: %v\n", err)
			return set.New[string]()
		}
	}
	keys, _, err := parseIndexBlob(blob)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cacheprog: web index parse: %v\n", err)
		if diskBlob != nil {
			return diskKeys
		}
		return set.New[string]()
	}
	b.writeIndexBlob(path, blob)
	return keys
}

// readDiskIndex returns (raw, parsed, etag) or (nil, empty, "") if the file
// is missing or invalid.
func (b *WebBackend) readDiskIndex(path string) ([]byte, set.Set[string], string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, set.New[string](), ""
	}
	keys, etag, err := parseIndexBlob(data)
	if err != nil {
		return nil, set.New[string](), ""
	}
	return data, keys, etag
}

// fetchIndexBlob does a conditional GET <endpoint>/<bucket>/_index. Returns:
//
//	body, http.StatusOK, nil          for a 200 response
//	nil,  http.StatusNotModified, nil for a 304 response
//	nil,  0, err                      for any transport failure or bad status
func (b *WebBackend) fetchIndexBlob(ifNoneMatch string) ([]byte, int, error) {
	req, err := http.NewRequest("GET", b.endpoint+"/"+b.bucket+"/_index", nil)
	if err != nil {
		return nil, 0, err
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	b.signRequest(req)
	resp, err := b.doRetryGET(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, 0, err
		}
		return body, http.StatusOK, nil
	case http.StatusNotModified:
		return nil, http.StatusNotModified, nil
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, 0, fmt.Errorf("HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
}

// writeIndexBlob persists a GBCI v1 blob via tmp file + atomic rename.
// Best-effort: failures are silently ignored, since a missing or stale on-disk
// cache only forces the next start to do a fresh GET, never affects correctness.
func (b *WebBackend) writeIndexBlob(path string, blob []byte) {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, blob, 0644); err != nil {
		return
	}
	os.Rename(tmp, path)
}

// parseIndexBlob validates a GBCI v1 blob and returns:
//
//   - the reconstructed key set (full S3 key strings)
//   - the strong ETag (hex-encoded SHA-256 trailer, RFC-7232 quoted)
//   - or an error if magic, version, length, or trailer hash don't validate.
func parseIndexBlob(blob []byte) (set.Set[string], string, error) {
	if len(blob) < gbciHeaderSize+sha256.Size {
		return set.Set[string]{}, "", fmt.Errorf("blob too small (%d bytes)", len(blob))
	}
	if !bytes.Equal(blob[0:4], gbciMagic[:]) {
		return set.Set[string]{}, "", fmt.Errorf("bad magic")
	}
	if blob[4] != gbciVersion {
		return set.Set[string]{}, "", fmt.Errorf("unsupported version %d", blob[4])
	}
	if blob[5] != gbciHashSize {
		return set.Set[string]{}, "", fmt.Errorf("unsupported hash size %d", blob[5])
	}
	count := binary.LittleEndian.Uint64(blob[16:24])
	bodyEnd := gbciHeaderSize + int(count)*gbciHashSize
	if bodyEnd+sha256.Size != len(blob) {
		return set.Set[string]{}, "", fmt.Errorf("length %d != header+%d*%d+trailer", len(blob), count, gbciHashSize)
	}
	expected := sha256.Sum256(blob[:bodyEnd])
	if !bytes.Equal(expected[:], blob[bodyEnd:]) {
		return set.Set[string]{}, "", fmt.Errorf("trailer hash mismatch")
	}
	keys := set.New[string]()
	hashHex := make([]byte, gbciHashSize*2)
	for i := uint64(0); i < count; i++ {
		off := gbciHeaderSize + int(i)*gbciHashSize
		hex.Encode(hashHex, blob[off:off+gbciHashSize])
		keys.Add(gbciKeyPrefix + string(hashHex))
	}
	etag := `"` + hex.EncodeToString(blob[bodyEnd:]) + `"`
	return keys, etag, nil
}

// marshalIndex encodes the given key set as a GBCI v1 blob. Keys not matching
// the cacheprog pattern are skipped. Used by tests.
func marshalIndex(keys set.Set[string]) []byte {
	hashes := make([][gbciHashSize]byte, 0, keys.Len())
	for k := range keys.All() {
		if h, ok := decodeActionHash(k); ok {
			hashes = append(hashes, h)
		}
	}
	sort.Slice(hashes, func(i, j int) bool {
		return bytes.Compare(hashes[i][:], hashes[j][:]) < 0
	})
	blob := make([]byte, gbciHeaderSize+len(hashes)*gbciHashSize+sha256.Size)
	copy(blob[0:4], gbciMagic[:])
	blob[4] = gbciVersion
	blob[5] = gbciHashSize
	binary.LittleEndian.PutUint16(blob[6:8], 0)
	binary.LittleEndian.PutUint64(blob[8:16], 0)
	binary.LittleEndian.PutUint64(blob[16:24], uint64(len(hashes)))
	off := gbciHeaderSize
	for i := range hashes {
		copy(blob[off:off+gbciHashSize], hashes[i][:])
		off += gbciHashSize
	}
	digest := sha256.Sum256(blob[:off])
	copy(blob[off:], digest[:])
	return blob
}

// decodeActionHash extracts the 32-byte action ID from a cacheprog cache key.
func decodeActionHash(key string) ([gbciHashSize]byte, bool) {
	var zero [gbciHashSize]byte
	if !strings.HasPrefix(key, gbciKeyPrefix) {
		return zero, false
	}
	hex64 := key[len(gbciKeyPrefix):]
	if len(hex64) != gbciHashSize*2 {
		return zero, false
	}
	var h [gbciHashSize]byte
	if _, err := hex.Decode(h[:], []byte(hex64)); err != nil {
		return zero, false
	}
	return h, true
}
