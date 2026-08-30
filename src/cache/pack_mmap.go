//go:build !cosmo

package cache

import (
	"os"

	mmap "github.com/wow-look-at-my/go-mmap"
)

// mapPackSpan maps the [off, off+length) body of pack file f read-only and returns the bytes plus a
// release func; the caller must not retain the slice past release. mmap offsets must be page-aligned,
// so it maps from the page boundary at or before the body and indexes in. ok=false means mapping
// failed and the caller falls back to a plain read.
//
// This is the only go-mmap call site; pack_mmap_cosmo.go is the GOOS=cosmo fallback (no cosmo port).
func mapPackSpan(f *os.File, off, length int64) (body []byte, release func(), ok bool) {
	pageSize := int64(os.Getpagesize())
	pageStart := off - off%pageSize
	span := off - pageStart + length
	m, err := mmap.MapRegion(int(f.Fd()), span, mmap.ProtRead, mmap.MapShared, pageStart)
	if err != nil {
		return nil, nil, false
	}
	_ = m.Advise(mmap.AdvSequential) // best-effort readahead hint for the linear scan
	bodyStart := int(off - pageStart)
	return []byte(m)[bodyStart : bodyStart+int(length)], func() { _ = m.Unmap() }, true
}
