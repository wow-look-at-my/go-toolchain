package cache

import (
	"io"
	"os"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// LocalStore is the process-local cache tier in front of the remote (web) backend.
//
// Every hit must return a DiskPath the Go toolchain can open and mmap directly — the
// GOCACHEPROG protocol hands the compiler a path (Response.DiskPath), never bytes.
//
// The implementations: LocalCache writes a loose body file plus a metadata
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
	// PutIfAbsent atomically stores body only if actionID isn't already cached, so a web-originated body never displaces a local body.
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

// NewLocalStore returns the local cache tier for dir.
//
// The loose-file cache is the default, because it is the only tier every
// go-toolchain binary can open. The packed tier needs a FUSE mount, go-fuse
// does not compile for GOOS=cosmo, and every shipped binary is a cosmo APE. So
// a default of packs gave a `go run ./src` build a packs/ store that the
// installed binary could not read: disjoint formats under the same cacheDir,
// each flavor starting cold behind the other. GOCACHE_FUSE=1 opts a build into
// packs, and is only worth setting where every run uses the same binary.
//
// The chosen tier is always logged. A build whose cache tier changes under it
// without a word is how that split went unnoticed.
func NewLocalStore(dir string) (LocalStore, error) {
	// Purge stale cache versions before either tier opens, so neither ever serves pre-purge data (see cacheversion.go).
	EnsureLocalCacheVersion(dir)

	if os.Getenv("GOCACHE_FUSE") != "1" {
		logger.Info("cacheprog: local cache: loose-file (set GOCACHE_FUSE=1 for the packed tier)")
		return NewLocalCache(dir)
	}
	fc, err := newFuseCache(dir)
	if err == nil {
		logger.Info("cacheprog: local cache: FUSE virtual filesystem (%s)", fc.mountInfo())
		return fc, nil
	}
	// Asked for and not delivered: say so, then fall back. The cache is an optimization.
	logger.Warn("cacheprog: GOCACHE_FUSE=1 but the packed tier is unavailable (%v); using the loose-file cache", err)
	return NewLocalCache(dir)
}
