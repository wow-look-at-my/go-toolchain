//go:build !windows

package build

// hideFile is a no-op off Windows, where a leading dot already hides a file.
func hideFile(string) {}

// revealFile is a no-op off Windows.
func revealFile(string) {}
