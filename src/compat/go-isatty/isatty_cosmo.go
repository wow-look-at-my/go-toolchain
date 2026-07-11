//go:build cosmo

package isatty

import "syscall"

// GOOS=cosmo (the gosmopolitan Go fork): upstream go-isatty selects no
// implementation file for cosmo, leaving the package empty and breaking every
// importer (fatih/color and friends). x/sys/unix has no cosmo port either, so
// the tcgets path is unavailable; approximate a terminal as "fd refers to a
// character device" via the fork's stdlib syscall.Fstat. This misclassifies
// e.g. /dev/null as a terminal, which for the consumers in this build graph
// (color enablement) is an acceptable tradeoff. Deliberately avoids
// os.NewFile, whose close-on-GC finalizer could close a live stdio fd.

// IsTerminal return true if the file descriptor is terminal.
func IsTerminal(fd uintptr) bool {
	var st syscall.Stat_t
	if err := syscall.Fstat(int(fd), &st); err != nil {
		return false
	}
	return st.Mode&syscall.S_IFMT == syscall.S_IFCHR
}

// IsCygwinTerminal return true if the file descriptor is a cygwin or msys2
// terminal. This is also always false on this environment.
func IsCygwinTerminal(fd uintptr) bool {
	return false
}
