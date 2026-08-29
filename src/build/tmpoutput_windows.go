//go:build windows

package build

import "syscall"

// hideFile marks a path hidden. A leading dot means nothing on Windows, so
// Explorer shows build/.tmp-<name> as if it were a finished binary. A missed
// attribute is cosmetic, never correctness.
func hideFile(path string) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return
	}
	_ = syscall.SetFileAttributes(p, syscall.FILE_ATTRIBUTE_HIDDEN)
}

// revealFile clears the hidden bit again; the attribute would otherwise ride
// the rename onto the final name and hide the shipped binary.
func revealFile(path string) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return
	}
	attrs, err := syscall.GetFileAttributes(p)
	if err != nil {
		return
	}
	_ = syscall.SetFileAttributes(p, attrs&^syscall.FILE_ATTRIBUTE_HIDDEN)
}
