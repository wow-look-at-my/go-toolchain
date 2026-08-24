package hostos

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// Detection is measured, not assumed: a sandbox can deny the filesystem probes, so a failed probe never returns a guess silently.

// hostSignalFunc is the seam for the gosmopolitan __hostos signal, declared in hostos_cosmo.go (only a cosmo build has one).

// hostosOut is where a guessed-host warning goes: stderr, since a consumer's stdout can be a protocol channel.
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

// Detection records the host OS and the evidence behind it. "linux" via uname
// and "linux" via a failed-probe DEFAULT are the same string but different
// facts -- the second is a guess, wrong on every Mac. Callers that need to
// trust the answer read Method.
type Detection struct {
	// OS is what GOOS() returns: "linux" or "darwin".
	OS string
	// Method names the evidence: compiled, uname, coreservices, procfs, or default (a guess).
	Method string
	// Uname is the sysname uname(2) reported, empty when it did not answer.
	Uname string
}

// Guessed reports whether every probe failed, leaving OS a fallback rather than a measurement.
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
