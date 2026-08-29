//go:build cosmo

package cmd

// cosmo has no persistent deps cache: sqlite's libc backend has no cosmo target; each check costs an HTTP request.

// noopDepsCache implements depsCache without persistence: every lookup misses, every store is dropped.
type noopDepsCache struct{}

func openDepsCache() (depsCache, error) { return noopDepsCache{}, nil }

func (noopDepsCache) lookup(path, version string) (update string, checkedAt int64, found bool) {
	return "", 0, false
}

func (noopDepsCache) store(path, version, update string, checkedAt int64) {}

func (noopDepsCache) close() {}
