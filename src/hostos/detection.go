package hostos

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// How much this answer can be trusted, measured rather than assumed.
//
// On a cosmo APE the first probe is dead weight on macOS: `syscall.Uname` is a
// raw SYS_UNAME the fork's darwin dispatcher has no case for, so it returns
// ENOSYS by design. Detection therefore rests on the filesystem probes, which
// are reads of absolute paths — and a sandbox can deny a read.
//
// Measured on macos-latest: `/System/Library/CoreServices` IS readable both
// under dats' seatbelt sandbox and outside it, so the answer is "darwin (via
// coreservices)" in both, by measurement. seatbelt restricts WRITES, not
// ordinary reads of system paths. The smoke jobs assert this on every run —
// inside the sandbox and outside — so a runner image or sandbox profile that
// ever does deny it fails CI instead of silently changing the answer.
//
// That failure would be expensive and invisible, which is why the guard exists
// at all: with no probe answering, Method is "default" and this reports LINUX
// ON A MAC, and then gobootstrap picks the wrong go.dev archive, cgoenv the
// wrong brew prefix, codeql the wrong platform dir, and the agent output guard
// the wrong classifier. So a guessed answer is never returned quietly —
// warnGuessedHost says so once, the way the guard announces a blind classifier.
//
// The standing improvement: the gosmopolitan runtime knows the host
// definitively via __hostos, set by rt0 from the APE boot path and used to
// dispatch every syscall, so it cannot be sandboxed away and cannot ENOSYS. It
// is being exported as runtime.CosmoHostOS(). When it lands, set
// hostSignalFunc to it and the probes become the fallback. That removes the
// last filesystem dependency here; it is not a prerequisite for anything.

// The seam for that signal is hostSignalFunc, declared in hostos_cosmo.go
// because only a cosmo build has a host to determine.

// hostosOut is where a guessed-host warning goes. stderr, never stdout: a
// consumer's stdout can be a protocol channel or a captured transcript. Held
// in a variable, which the bannedoutput analyzer deliberately permits.
var hostosOut io.Writer = os.Stderr

var guessedHostOnce sync.Once

// guessedHostBanner is the warning, held as one document. Its only value is
// the OS the fallback assumed.
const guessedHostBanner = "\n⚠ go-toolchain could not determine its HOST OS and is assuming %q.\n" +
	"Every probe failed: uname is unimplemented for this target and the filesystem\n" +
	"probes were denied or absent (a sandbox will do that). On a Mac this answer is\n" +
	"WRONG, and host-specific choices -- Go toolchain archives, brew paths, the agent\n" +
	"output guard -- are being made on it. Run `go-toolchain version host` to see.\n\n"

// warnGuessedHost reports, once per run, that the host OS is a fallback rather
// than a measurement, and names what that costs.
func warnGuessedHost(d Detection) {
	guessedHostOnce.Do(func() {
		fmt.Fprintf(hostosOut, guessedHostBanner, d.OS)
	})
}

// Detection records the host OS and the evidence behind it.
//
// The answer alone is not auditable: a cosmo APE probes for its host, and a
// probe that fails falls back to a DEFAULT. "linux" because uname said so and
// "linux" because nothing answered are the same string and very different
// facts — the second is a guess that is wrong on every Mac. Callers that need
// to trust the answer (and the CI that pins it) read Method.
type Detection struct {
	// OS is what GOOS() returns: "linux" or "darwin".
	OS string
	// Method names the evidence: "compiled" (not a cosmo build, the compiler
	// knew), "uname", "coreservices", "procfs", or "default" — the last
	// meaning every probe failed and OS is a guess.
	Method string
	// Uname is the sysname uname(2) reported, empty when it did not answer.
	Uname string
}

// Guessed reports whether every probe failed, leaving OS a fallback rather
// than a measurement.
func (d Detection) Guessed() bool { return d.Method == "default" }

// String renders the detection for `go-toolchain version host`.
func (d Detection) String() string {
	s := fmt.Sprintf("host: %s (via %s)", d.OS, d.Method)
	if d.Uname != "" {
		s += fmt.Sprintf(", uname: %s", d.Uname)
	}
	if d.Guessed() {
		s += " -- GUESSED: no probe answered"
	}
	return s
}
