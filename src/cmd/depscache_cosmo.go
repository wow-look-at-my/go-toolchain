//go:build cosmo

package cmd

// GOOS=cosmo builds carry no persistent dependency-check cache:
// modernc.org/sqlite depends on modernc.org/libc, hundreds of thousands of
// lines of per-GOOS generated libc code with no cosmo target, so the sqlite
// backend (depscache_sqlite.go) cannot compile here. Tradeoff accepted: the
// cache only dedups module-proxy @latest lookups (up-to-date entries expire
// after one minute anyway), so an uncached check costs one HTTP request per
// pseudo-versioned direct dependency per run — small, and far cheaper than
// porting a generated libc. Swap in a flat-file cache if this ever hurts.

// noopDepsCache implements depsCache without persistence: every lookup
// misses, every store is dropped.
type noopDepsCache struct{}

func openDepsCache() (depsCache, error) { return noopDepsCache{}, nil }

func (noopDepsCache) lookup(path, version string) (update string, checkedAt int64, found bool) {
	return "", 0, false
}

func (noopDepsCache) store(path, version, update string, checkedAt int64) {}

func (noopDepsCache) close() {}
