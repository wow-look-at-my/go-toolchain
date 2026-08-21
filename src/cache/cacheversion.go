package cache

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// currentLocalCacheVersion stamps the content generation of the local cache
// root (the "buildcache" dir holding both tiers: the pack store under packs/
// and the loose-file tier's 00..ff bucket dirs). When the stamp on disk
// differs, EnsureLocalCacheVersion deletes the cached DATA once and re-stamps,
// mirroring go-s3-server's currentCacheVersion. Bump this to force every
// machine to shed cache contents that a code change has made untrustworthy.
//
// Version history:
//
//	1: implicit — any root without a stamp file.
//	2: the local tiers began SERVING Go module indexes (see verify.go). That is
//	   sound only for locally-originated indexes, and the web ingestion guards
//	   that enforce local origin (refusing index blobs on the individual GET,
//	   batch GET, and prefetch paths) did not always exist — a root populated
//	   by an older binary may hold web-originated, potentially mis-keyed
//	   indexes, and pack bytes are otherwise immortal (evictions only drop
//	   in-memory index entries; scanPack re-indexes everything at startup).
//	   The purge also reclaims the duplicate-record bloat the old
//	   accept-at-Put/refuse-at-Get modindex loop appended to the packs.
const currentLocalCacheVersion = 2

// localCacheVersionFile is the version stamp, directly under the cache root.
const localCacheVersionFile = ".cache_version"

// fuseLockName is the exclusive-owner lock file under the cache root. The FUSE
// daemon flocks it for its lifetime (see newFuseCache); the version purge
// flocks it briefly so it never deletes pack files out from under a live owner.
const fuseLockName = ".fuse.lock"

// EnsureLocalCacheVersion runs the one-time local cache purge for root (the
// buildcache dir) when the on-disk version stamp does not match
// currentLocalCacheVersion. It is called before either local tier opens —
// NewLocalStore for the daemon (FUSE, loose fallback, and GOCACHE_NO_FUSE) and
// runCacheProg for the standalone cacheprog — so no tier ever serves pre-purge
// data. Everything is best-effort: the cache is an optimization, so failures
// are logged and the build proceeds (an unwritten stamp retries next run).
//
// The purge deletes only the known DATA children of root — the packs/ dir, the
// loose tier's two-hex-digit bucket dirs, and stray temp files. It never
// touches mnt/ (a possibly-mounted FUSE mountpoint), the lock file, or any
// unknown name, and by construction never reaches outside root (siblings like
// deps.db or downloaded toolchains live outside the buildcache root).
//
// A fresh or already-current root writes/keeps the stamp silently; the one
// stderr line is printed only when data was actually removed.
func EnsureLocalCacheVersion(root string) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		logger.Warn("cacheprog: local cache: version check: %v", err)
		return
	}
	stamp := filepath.Join(root, localCacheVersionFile)
	stored := readLocalCacheVersion(stamp)
	if stored == currentLocalCacheVersion {
		return
	}

	// Take the same exclusive flock the FUSE daemon holds for its lifetime:
	// purging while another live process owns the mount + pack store would
	// delete pack files out from under it. When the lock is busy the purge is
	// simply deferred — the stamp stays unwritten, so the next run (with no
	// live owner) performs it.
	release, ok := lockCacheRootForPurge(root)
	if !ok {
		logger.Info("cacheprog: local cache: another process owns %s; deferring version purge to the next run", root)
		return
	}
	defer release()

	purged, err := purgeLocalCacheData(root)
	if err != nil {
		// Leave the stamp unwritten so the next run retries the purge.
		logger.Warn("cacheprog: local cache: version %d -> %d purge: %v (will retry next run)",
			stored, currentLocalCacheVersion, err)
		return
	}
	if err := writeLocalCacheVersion(stamp); err != nil {
		logger.Warn("cacheprog: local cache: writing version stamp: %v", err)
		return
	}
	if purged {
		logger.Info("cacheprog: local cache: purged (cache version %d -> %d: pre-guard module-index residue)",
			stored, currentLocalCacheVersion)
	}
}

// readLocalCacheVersion returns the version recorded in the stamp file, or 1
// (the implicit pre-stamp version) when the file is missing or unparseable.
func readLocalCacheVersion(stamp string) int {
	raw, err := os.ReadFile(stamp)
	if err != nil {
		return 1
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 1
	}
	return v
}

// writeLocalCacheVersion atomically (temp + rename) records the current
// version in stamp. The temp name carries the ".tmp-" prefix so a leftover
// from a crash is itself collected by a future purge.
func writeLocalCacheVersion(stamp string) error {
	tmp, err := os.CreateTemp(filepath.Dir(stamp), ".tmp-cache_version-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, werr := tmp.WriteString(strconv.Itoa(currentLocalCacheVersion) + "\n")
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmpName)
		if werr != nil {
			return werr
		}
		return cerr
	}
	if err := os.Rename(tmpName, stamp); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// purgeLocalCacheData deletes the cached data under root: the pack store's
// packs/ dir, the loose tier's 00..ff bucket dirs, and stray temp files. It
// operates ONLY on known child names — mnt/ (a possibly-mounted FUSE
// mountpoint), the lock file, the stamp, and anything unrecognized survive —
// and therefore can never reach outside root. Returns whether anything was
// actually removed. Already-missing entries are tolerated.
func purgeLocalCacheData(root string) (purged bool, err error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false, err
	}
	var firstErr error
	for _, e := range entries {
		name := e.Name()
		var victim bool
		switch {
		case e.IsDir():
			victim = name == "packs" || isLooseBucketName(name)
		default:
			victim = strings.HasPrefix(name, ".tmp-") || strings.HasSuffix(name, ".tmp")
		}
		if !victim {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		purged = true
	}
	return purged, firstErr
}

// isLooseBucketName reports whether name is one of the loose tier's 256
// two-hex-digit bucket dirs (see NewLocalCache / LocalCache.dataPath).
func isLooseBucketName(name string) bool {
	if len(name) != 2 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
