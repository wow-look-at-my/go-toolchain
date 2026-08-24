package cache

import (
	"errors"
	"io"
	"os"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// LocalStore is the process-local cache tier in front of the remote (web) backend.
//
// Every hit must return a DiskPath the Go toolchain can open and mmap directly — the
// GOCACHEPROG protocol hands the compiler a path (Response.DiskPath), never bytes.
//
// Two implementations exist: LocalCache writes one loose body file plus a metadata
// sidecar (the portable fallback), and FuseCache packs bodies into append-only pack
// files behind a read-only FUSE mount, materializing each body on read with no loose
// file or sidecar per entry.
type LocalStore interface {
	// Get returns the cached entry for actionID, or miss == true.
	Get(actionID string) (CacheMeta, bool)
	// Peek is Get without counting a hit, used by PUT dedup so a warm rebuild does not inflate the hit rate.
	Peek(actionID string) (CacheMeta, bool)
	// Put stores body under actionID/outputID and returns a DiskPath the Go toolchain can open.
	Put(actionID, outputID string, body io.Reader) (string, error)
	// PutIfAbsent atomically stores body only if actionID isn't already cached, so a web-originated body never displaces a local one.
	PutIfAbsent(actionID, outputID string, body io.Reader) (stored bool, err error)
	// StatsPtr returns the live hit/put counters for this store.
	StatsPtr() *CacheStats
	// Close releases resources. For FuseCache this unmounts the filesystem.
	Close() error
}

// fuseStore is a LocalStore backed by a FUSE mount, plus mountInfo so NewLocalStore can
// log where it is mounted.
type fuseStore interface {
	LocalStore
	mountInfo() string
}

// errFuseUnsupported signals a platform with no FUSE support; NewLocalStore falls back to the loose-file cache silently.
var errFuseUnsupported = errors.New("FUSE not supported on this platform")

// errFuseBusy signals another process already owns the FUSE mount; the caller falls back to the loose cache silently.
var errFuseBusy = errors.New("FUSE cache already owned by another process")

// NewLocalStore returns the preferred local cache for dir: a FUSE-backed packed
// store when the platform supports it and the mount succeeds, otherwise the
// loose-file LocalCache.
//
// The returned store is always usable. A FUSE failure (missing /dev/fuse, no
// permission, no fusermount helper, an unsupported platform) degrades to the
// loose cache rather than failing the build — the cache is an optimization,
// never a correctness dependency.
func NewLocalStore(dir string) (LocalStore, error) {
	// Purge stale cache versions before either tier opens, so neither ever serves pre-purge data (see cacheversion.go).
	EnsureLocalCacheVersion(dir)

	// Escape hatch: force the loose-file cache, skipping FUSE entirely, if a mount misbehaves.
	if os.Getenv("GOCACHE_NO_FUSE") == "1" {
		logger.Info("cacheprog: local cache: loose-file (GOCACHE_NO_FUSE=1)")
		return NewLocalCache(dir)
	}
	fc, err := newFuseCache(dir)
	if err == nil {
		logger.Info("cacheprog: local cache: FUSE virtual filesystem (%s)", fc.mountInfo())
		return fc, nil
	}
	if !errors.Is(err, errFuseUnsupported) && !errors.Is(err, errFuseBusy) {
		logger.Warn("cacheprog: FUSE cache unavailable (%v); using loose-file cache", err)
	}
	return NewLocalCache(dir)
}
