//go:build windows

package build

import "syscall"

// hideFile marks a path hidden: on Windows a leading dot carries no meaning
// (Unix's "dotfiles are invisible by convention" is not a thing there), and
// Explorer would show the in-flight build output build/.tmp-<name> as if it
// were a finished binary. The attribute is set as soon as the toolchain owns
// the file — cmd/go only materializes its -o at the very end of the build —
// so the visible window is small; a missed attribute is cosmetic, never
// correctness.
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