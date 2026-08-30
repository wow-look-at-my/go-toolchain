//go:build cosmo

package hostos

import (
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
)

// hostGOOS is memoized: the host cannot change under a running process.
var hostGOOS = sync.OnceValue(detectHostGOOS)

// hostSignalFunc is the authoritative signal, checked before any probe. Empty means none; see detection.go.
var hostSignalFunc = cosmoHostSignal

// cosmoHostSignal reports the host the APE entry stub recorded, or "" for a
// host the fork's runtime has no port for. The stub runs before any Go code
// and every syscall dispatches on the same value, so no sandbox can deny it
// and no target can ENOSYS it -- which is what the probes below cannot say.
func cosmoHostSignal() string {
	if host := runtime.CosmoHostOS(); host != "unknown" {
		return host
	}
	return ""
}

// GOOS returns the host OS: "linux", "darwin" or "windows". A fat APE runs on all three.
func GOOS() string { return hostGOOS().OS }

// Detect returns the host OS plus how it was determined (see go-toolchain version host). Memoized; the probe runs a single time.
func Detect() Detection { return hostGOOS() }

func detectHostGOOS() Detection {
	// An authoritative signal outranks every probe: it cannot be denied by a sandbox or ENOSYS. See detection.go.
	if host := hostSignalFunc(); host != "" {
		return Detection{OS: host, Method: "runtime"}
	}

	// uname: Sysname is authoritative on Linux; macOS ENOSYS's it via the fork's darwin dispatcher and falls through.
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

	// CoreServices=macOS, procfs=Linux. A denying sandbox falls silently to
	// "linux" (Method records which); smoke jobs assert both cases.
	if _, err := os.Stat("/System/Library/CoreServices"); err == nil {
		return Detection{OS: "darwin", Method: "coreservices", Uname: sysname}
	}
	if _, err := os.Stat("/proc/self"); err == nil {
		return Detection{OS: "linux", Method: "procfs", Uname: sysname}
	}
	// Nothing answered; "linux" is a GUESS, wrong on every Mac. It announces itself via warnGuessedHost, and Method="default".
	d := Detection{OS: "linux", Method: "default", Uname: sysname}
	warnGuessedHost(d)
	return d
}

// cstring returns b up to its leading NUL (or all of b) -- Utsname fields are fixed-size NUL-terminated buffers.
func cstring(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
