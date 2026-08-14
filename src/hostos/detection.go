package hostos

import "fmt"

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
