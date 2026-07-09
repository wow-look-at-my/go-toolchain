package cache

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// LocalStore is the process-local cache tier that sits in front of the remote
// (web) backend.
//
// Every hit must return a DiskPath the Go toolchain can open and read
// directly. That is the one hard constraint the GOCACHEPROG protocol places on
// a cache: a GET response hands the compiler a *path* (Response.DiskPath), not
// the bytes, and the compiler opens and mmaps it itself. A PUT body, by
// contrast, arrives inline over the protocol (base64 after the JSON line).
//
// Two implementations exist:
//
//   - LocalCache writes one loose body file per entry plus a metadata sidecar.
//     Simple and portable; it is the fallback used when FUSE is unavailable.
//
//   - FuseCache packs every body into append-only pack files and exposes them
//     through a read-only FUSE mount. DiskPath points into the mount and the
//     kernel materializes each body virtually on read, so there is no loose
//     file and no sidecar per entry. This is the "virtual filesystem over the
//     JSON protocol": it satisfies the DiskPath contract without ever writing a
//     file per cache entry.
type LocalStore interface {
	// Get returns the cached entry for actionID, or miss == true.
	Get(actionID string) (CacheMeta, bool)
	// Peek is Get without counting a hit. The PUT dedup check uses it: a PUT
	// that finds its action already stored serves the existing entry, but
	// counting that as a cache "hit" inflated the hit rate on warm rebuilds
	// (the caller just COMPUTED the object; nothing was saved). Verification
	// and eviction semantics are identical to Get.
	Peek(actionID string) (CacheMeta, bool)
	// Put stores body under actionID/outputID and returns a DiskPath that the
	// Go toolchain can open.
	Put(actionID, outputID string, body io.Reader) (string, error)
	// StatsPtr returns the live hit/put counters for this store.
	StatsPtr() *CacheStats
	// Close releases resources. For FuseCache this unmounts the filesystem.
	Close() error
}

// fuseStore is a LocalStore backed by a FUSE mount. The extra mountInfo method
// lets NewLocalStore log a useful one-liner about where the virtual filesystem
// is mounted. newFuseCache returns this concrete-ish type so the platform
// stub (which returns a nil fuseStore) and the real implementation share a
// signature.
type fuseStore interface {
	LocalStore
	mountInfo() string
}

// errFuseUnsupported is returned by newFuseCache on platforms without a FUSE
// implementation (e.g. Windows). It signals NewLocalStore to fall back to the
// loose-file cache silently, without logging a scary error.
var errFuseUnsupported = errors.New("FUSE not supported on this platform")

// errFuseBusy is returned by newFuseCache when another live process already
// owns the FUSE mount + pack store for this cache dir (it holds the lock). The
// second caller must NOT touch the mount; it falls back to the loose cache.
// This is the normal, expected outcome for a nested go-toolchain run (e.g. in
// tests) or a concurrent standalone invocation, so the fallback is silent.
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
	// One-time cache version purge, BEFORE the FUSE mount and before either
	// tier opens (packs/ and the loose bucket dirs share this root), so no
	// tier ever serves pre-purge data. See cacheversion.go. The standalone
	// cacheprog path, which bypasses NewLocalStore on purpose (it must never
	// grab the FUSE mount), calls this itself — see cmd/cacheprog.go.
	EnsureLocalCacheVersion(dir)

	// Escape hatch: force the loose-file cache, skipping FUSE entirely. Lets an
	// operator sidestep the FUSE tier wholesale if a mount misbehaves in some
	// environment, without code changes.
	if os.Getenv("GOCACHE_NO_FUSE") == "1" {
		fmt.Fprintf(os.Stderr, "cacheprog: local cache: loose-file (GOCACHE_NO_FUSE=1)\n")
		return NewLocalCache(dir)
	}
	fc, err := newFuseCache(dir)
	if err == nil {
		fmt.Fprintf(os.Stderr, "cacheprog: local cache: FUSE virtual filesystem (%s)\n", fc.mountInfo())
		return fc, nil
	}
	if !errors.Is(err, errFuseUnsupported) && !errors.Is(err, errFuseBusy) {
		fmt.Fprintf(os.Stderr, "cacheprog: FUSE cache unavailable (%v); using loose-file cache\n", err)
	}
	return NewLocalCache(dir)
}
