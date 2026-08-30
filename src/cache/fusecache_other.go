//go:build cosmo || (!linux && !darwin)

package cache

import "errors"

// errFuseUnsupported signals a platform with no FUSE support.
var errFuseUnsupported = errors.New("FUSE not supported on this platform")

// newFuseCache is unavailable on platforms without FUSE (cosmo, Windows); NewLocalStore uses LocalCache.
func newFuseCache(cacheDir string) (fuseStore, error) {
	return nil, errFuseUnsupported
}
