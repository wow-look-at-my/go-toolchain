//go:build cosmo || (!linux && !darwin)

package cache

// No-op without the FUSE tier (Windows, cosmo): no mount owner to protect, so purge always grants the lock.
func lockCacheRootForPurge(string) (release func(), ok bool) {
	return func() {}, true
}
