//go:build cosmo

package cache

import "os"

// mapPackSpan is the cosmo stand-in for pack_mmap.go: go-mmap has no cosmo
// port, so the body is pread into a heap buffer. Not zero-copy, but fine
// since FUSE cache is compiled out under cosmo.
func mapPackSpan(f *os.File, off, length int64) (body []byte, release func(), ok bool) {
	buf := make([]byte, length)
	if _, err := f.ReadAt(buf, off); err != nil {
		return nil, nil, false
	}
	return buf, func() {}, true
}
