//go:build !windows

package build

// hideFile is a no-op off Windows: a ".tmp-" prefix is already
// conventionally invisible there. It exists so the temp file never carries a
// stray hidden attribute onto Windows, and so CommitOutput needs no build
// tags of its own.
func hideFile(string) {}

// revealFile is a no-op off Windows.
func revealFile(string) {}
