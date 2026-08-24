package cache

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// LocalCache is a filesystem-based build cache. Objects are stored in a
// two-level directory hierarchy keyed by the hex-encoded action ID.
type LocalCache struct {
	dir   string
	Stats CacheStats

	vmu      sync.RWMutex
	verified map[string]looseVerified // actionID -> verified (outputID, size)

	// plocks stripes per-action write locks, making PutIfAbsent's check atomic.
	plocks [64]sync.Mutex
}

// NewLocalCache creates a local cache rooted at dir. It pre-creates 256
// subdirectories (00–ff) so that later writes never need to mkdir.
func NewLocalCache(dir string) (*LocalCache, error) {
	for i := 0; i < 256; i++ {
		sub := filepath.Join(dir, fmt.Sprintf("%02x", i))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			return nil, fmt.Errorf("cache init: %w", err)
		}
	}
	return &LocalCache{dir: dir, verified: make(map[string]looseVerified)}, nil
}

// CacheMeta holds the metadata stored alongside a cached object.
type CacheMeta struct {
	OutputID string
	Size     int64
	Time     time.Time
	DiskPath string
}

// Get looks up actionID. A hit is integrity-verified (verifyBodyForServe)
// before serving; a failing entry is evicted and reported as a miss so the
// toolchain recomputes it. Verification is memoized per process.
func (c *LocalCache) Get(actionID string) (meta CacheMeta, miss bool) {
	return c.get(actionID, true)
}

// Peek is Get without counting a hit — see LocalStore.Peek.
func (c *LocalCache) Peek(actionID string) (meta CacheMeta, miss bool) {
	return c.get(actionID, false)
}

func (c *LocalCache) get(actionID string, countHit bool) (meta CacheMeta, miss bool) {
	dataPath := c.dataPath(actionID)
	metaPath := dataPath + ".meta"

	raw, err := os.ReadFile(metaPath)
	if err != nil {
		return CacheMeta{}, true
	}

	m, err := parseMeta(string(raw))
	if err != nil {
		return CacheMeta{}, true
	}

	// Verify the data file still exists.
	info, err := os.Stat(dataPath)
	if err != nil {
		return CacheMeta{}, true
	}
	m.DiskPath = dataPath

	// Fast path: skip the body re-read if this (outputID, size) already matched.
	c.vmu.RLock()
	v, vok := c.verified[actionID]
	c.vmu.RUnlock()
	if vok && v.outputID == m.OutputID && v.size == info.Size() {
		m.Size = v.size
		if countHit {
			c.Stats.Hits.Increment()
		}
		return m, false
	}

	body, err := os.ReadFile(dataPath)
	if err != nil {
		return CacheMeta{}, true
	}
	if reason, ok := verifyBodyForServe(actionID, m.OutputID, body); !ok {
		// Evict data + sidecar and miss, so the toolchain recomputes clean data.
		os.Remove(dataPath)
		os.Remove(metaPath)
		c.vmu.Lock()
		delete(c.verified, actionID)
		c.vmu.Unlock()
		c.Stats.Corrupt.Increment()
		logger.Warn("cacheprog: local cache: evicting %s: %s; treating as miss", shortID(actionID), reason)
		return CacheMeta{}, true
	}
	// Serve the verified byte count, never the raw stat size (masks truncation).
	m.Size = int64(len(body))
	c.vmu.Lock()
	c.verified[actionID] = looseVerified{outputID: m.OutputID, size: m.Size}
	c.vmu.Unlock()
	if countHit {
		c.Stats.Hits.Increment()
	}
	return m, false
}

// Put writes body under actionID and stores the metadata sidecar, returning
// the absolute disk path. Writes for one action are serialized on a striped
// lock so PutIfAbsent's existence check cannot interleave with a concurrent
// Put (see LocalStore.PutIfAbsent).
func (c *LocalCache) Put(actionID, outputID string, body io.Reader) (string, error) {
	l := c.plock(actionID)
	l.Lock()
	defer l.Unlock()
	return c.putLocked(actionID, outputID, body)
}

// PutIfAbsent stores body only if actionID is not already cached — see
// LocalStore.PutIfAbsent. The existence check and the write happen under the
// same per-action lock Put uses, so the two cannot interleave.
func (c *LocalCache) PutIfAbsent(actionID, outputID string, body io.Reader) (bool, error) {
	l := c.plock(actionID)
	l.Lock()
	defer l.Unlock()
	// Peek semantics: a present-and-servable entry blocks the store; a corrupt
	// one was just evicted by the check and is fair game to fill.
	if _, miss := c.get(actionID, false); !miss {
		return false, nil
	}
	if _, err := c.putLocked(actionID, outputID, body); err != nil {
		return false, err
	}
	return true, nil
}

// plock returns the striped write lock for actionID (allocation-free FNV-1a).
func (c *LocalCache) plock(actionID string) *sync.Mutex {
	h := uint32(2166136261)
	for i := 0; i < len(actionID); i++ {
		h ^= uint32(actionID[i])
		h *= 16777619
	}
	return &c.plocks[h%uint32(len(c.plocks))]
}

func (c *LocalCache) putLocked(actionID, outputID string, body io.Reader) (string, error) {
	dataPath := c.dataPath(actionID)

	// Atomic write: temp file in same directory, then rename.
	dir := filepath.Dir(dataPath)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", fmt.Errorf("cache put: %w", err)
	}
	tmpName := tmp.Name()

	n, copyErr := io.Copy(tmp, body)
	closeErr := tmp.Close()
	if copyErr != nil {
		os.Remove(tmpName)
		return "", fmt.Errorf("cache put copy: %w", copyErr)
	}
	if closeErr != nil {
		os.Remove(tmpName)
		return "", fmt.Errorf("cache put close: %w", closeErr)
	}

	if err := os.Rename(tmpName, dataPath); err != nil {
		os.Remove(tmpName)
		return "", fmt.Errorf("cache put rename: %w", err)
	}

	// Write metadata sidecar (also atomic).
	now := time.Now()
	meta := fmt.Sprintf("outputID:%s\nsize:%d\ntime:%d\n", outputID, n, now.Unix())
	metaPath := dataPath + ".meta"
	metaTmp, err := os.CreateTemp(dir, ".meta-*")
	if err != nil {
		return dataPath, nil // data written, metadata lost — acceptable
	}
	metaTmpName := metaTmp.Name()
	_, _ = metaTmp.WriteString(meta)
	metaTmp.Close()
	if err := os.Rename(metaTmpName, metaPath); err != nil {
		os.Remove(metaTmpName)
	}

	// Content changed: drop the memoized verification so Get re-verifies it.
	c.vmu.Lock()
	delete(c.verified, actionID)
	c.vmu.Unlock()

	c.Stats.Puts.Increment()
	return dataPath, nil
}

// StatsPtr returns the live hit/put counters, satisfying LocalStore.
func (c *LocalCache) StatsPtr() *CacheStats { return &c.Stats }

// Close is a nop; it exists to satisfy LocalStore (FuseCache uses it to unmount).
func (c *LocalCache) Close() error { return nil }

// dataPath returns the absolute path for a cached object.
// Layout: dir/{first-byte-hex}/v1{actionID}
func (c *LocalCache) dataPath(actionID string) string {
	bucket := "00"
	if len(actionID) >= 2 {
		bucket = actionID[:2]
	}
	return filepath.Join(c.dir, bucket, "v1"+actionID)
}

// parseMeta parses the key:value metadata sidecar format.
func parseMeta(raw string) (CacheMeta, error) {
	var m CacheMeta
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch k {
		case "outputID":
			m.OutputID = v
		case "size":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return CacheMeta{}, err
			}
			m.Size = n
		case "time":
			unix, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return CacheMeta{}, err
			}
			m.Time = time.Unix(unix, 0)
		}
	}
	if m.OutputID == "" {
		return CacheMeta{}, fmt.Errorf("missing outputID in metadata")
	}
	return m, nil
}
