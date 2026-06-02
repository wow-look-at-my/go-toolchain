//go:build !linux && !darwin

package cache

// newFuseCache is unavailable on platforms without a FUSE implementation
// (e.g. Windows). NewLocalStore falls back to the loose-file LocalCache.
func newFuseCache(cacheDir string) (fuseStore, error) {
	return nil, errFuseUnsupported
}
