//go:build cosmo

package cache

import "os"

// mapPackSpan is the GOOS=cosmo stand-in for the mmap fast path in
// pack_mmap.go: wow-look-at-my/go-mmap has no cosmo implementation, so the
// body is pread into a heap buffer instead. Correct but not zero-copy —
// acceptable because the FUSE cache is compiled out under cosmo
// (fusecache_other.go), so PackStore is never on the hot serve path there.
func mapPackSpan(f *os.File, off, length int64) (body []byte, release func(), ok bool) {
	buf := make([]byte, length)
	if _, err := f.ReadAt(buf, off); err != nil {
		return nil, nil, false
	}
	return buf, func() {}, true
}
