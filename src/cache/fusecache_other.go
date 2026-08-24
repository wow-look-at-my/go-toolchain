//go:build cosmo || (!linux && !darwin)

package cache

// newFuseCache is unavailable on platforms without FUSE (e.g. Windows); NewLocalStore falls back to LocalCache.
func newFuseCache(cacheDir string) (fuseStore, error) {
	return nil, errFuseUnsupported
}
