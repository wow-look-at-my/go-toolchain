//go:build cosmo

package hostos

import (
	"os"
	"strings"
	"sync"
	"syscall"
)

// A GOOS=cosmo binary reports runtime.GOOS == "cosmo" on every host, so the
// host OS must be probed at runtime. Detection is memoized: the host cannot
// change under a running process.
var hostGOOS = sync.OnceValue(detectHostGOOS)

// GOOS returns the operating system of the host this cosmo binary is running
// on: "linux" or "darwin". Never "windows" — on Windows hosts a fat APE
// executes its embedded native GOOS=windows payload, which compiles the
// non-cosmo variant of this package instead.
func GOOS() string { return hostGOOS() }

func detectHostGOOS() string {
	// uname(2) first: the fork's stdlib syscall exposes Uname for cosmo. On
	// Linux hosts the raw (Linux-numbered) syscall passes straight through and
	// Sysname is authoritative. On macOS hosts unemulated syscalls return
	// ENOSYS from the fork's darwin dispatcher — no crash — so a failure just
	// falls through to the filesystem probes.
	var uts syscall.Utsname
	if err := syscall.Uname(&uts); err == nil {
		switch strings.ToLower(cstring(uts.Sysname[:])) {
		case "linux":
			return "linux"
		case "darwin", "xnu":
			return "darwin"
		}
	}

	// Filesystem probes. /System/Library/CoreServices exists on every macOS
	// install and never on Linux; procfs is Linux-only. Default to linux —
	// the most common host and the safer guess for path conventions.
	if _, err := os.Stat("/System/Library/CoreServices"); err == nil {
		return "darwin"
	}
	if _, err := os.Stat("/proc/self"); err == nil {
		return "linux"
	}
	return "linux"
}

// cstring returns the string up to the first NUL in b (the whole slice if
// there is none) — Utsname fields are fixed-size NUL-terminated buffers.
func cstring(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
