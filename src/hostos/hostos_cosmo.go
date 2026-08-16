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

// hostSignalFunc is the authoritative host signal when one exists, consulted
// before any probe. An empty result means "no authoritative signal here" and
// falls through to the probes. This is the seam runtime.CosmoHostOS() plugs
// into — see the KNOWN DEFECT note in detection.go for why the probes alone
// are not enough on a Mac.
var hostSignalFunc func() string

// GOOS returns the operating system of the host this cosmo binary is running
// on: "linux" or "darwin". Never "windows" — on Windows hosts a fat APE
// executes its embedded native GOOS=windows payload, which compiles the
// non-cosmo variant of this package instead.
func GOOS() string { return hostGOOS().OS }

// Detect returns the host OS together with the evidence behind it, so a caller
// — or `go-toolchain version host` — can tell a measurement from the fallback
// guess. Same memoized probe GOOS() uses; it never runs twice.
func Detect() Detection { return hostGOOS() }

func detectHostGOOS() Detection {
	// An authoritative signal, when the toolchain provides one, outranks every
	// probe below: it cannot be denied by a sandbox and cannot ENOSYS. See the
	// KNOWN DEFECT note in detection.go for why the probes are not enough.
	if hostSignalFunc != nil {
		if host := hostSignalFunc(); host != "" {
			return Detection{OS: host, Method: "runtime"}
		}
	}

	// uname(2) first: the fork's stdlib syscall exposes Uname for cosmo. On
	// Linux hosts the raw (Linux-numbered) syscall passes straight through and
	// Sysname is authoritative. On macOS hosts unemulated syscalls return
	// ENOSYS from the fork's darwin dispatcher — no crash — so a failure just
	// falls through to the filesystem probes.
	var uts syscall.Utsname
	var sysname string
	if err := syscall.Uname(&uts); err == nil {
		sysname = cstring(uts.Sysname[:])
		switch strings.ToLower(sysname) {
		case "linux":
			return Detection{OS: "linux", Method: "uname", Uname: sysname}
		case "darwin", "xnu":
			return Detection{OS: "darwin", Method: "uname", Uname: sysname}
		}
	}

	// Filesystem probes. /System/Library/CoreServices exists on every macOS
	// install and never on Linux; procfs is Linux-only.
	//
	// These are the weak link: both are READS of an absolute path, and a
	// sandbox can deny them. Under one that does, neither answers and the
	// fallback below claims "linux" on a Mac — which is why Method is recorded
	// and why the smoke jobs assert it from INSIDE dats' sandbox as well as
	// outside. A wrong host here silently mis-picks every host-specific
	// resource: toolchain archives, brew paths, and the agent output guard's
	// whole classifier.
	if _, err := os.Stat("/System/Library/CoreServices"); err == nil {
		return Detection{OS: "darwin", Method: "coreservices", Uname: sysname}
	}
	if _, err := os.Stat("/proc/self"); err == nil {
		return Detection{OS: "linux", Method: "procfs", Uname: sysname}
	}
	// Nothing answered. "linux" is the most common host and the safer guess
	// for path conventions, but it is a GUESS, it is wrong on every Mac, and
	// consumers act on it — so it announces itself rather than being returned
	// quietly. Method says so too, and Guessed() lets a caller that must not
	// act on a guess find out.
	d := Detection{OS: "linux", Method: "default", Uname: sysname}
	warnGuessedHost(d)
	return d
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
