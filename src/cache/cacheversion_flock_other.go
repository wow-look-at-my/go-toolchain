//go:build cosmo || (!linux && !darwin)

package cache

// lockCacheRootForPurge is a no-op on platforms without the FUSE tier
// (Windows, GOOS=cosmo — see fusecache_other.go): there is no long-lived
// exclusive mount owner to protect, only loose-file writers, for which a purge
// is equivalent to any external cleanup of the cache dir mid-run (a tolerated
// miss). Always grants the "lock".
func lockCacheRootForPurge(string) (release func(), ok bool) {
	return func() {}, true
}
