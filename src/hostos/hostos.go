//go:build !cosmo

// Package hostos reports the operating system of the HOST the binary is
// actually running on, as opposed to runtime.GOOS, which reports what the
// binary was compiled for. The two differ only for GOOS=cosmo fat APEs built
// with the gosmopolitan fork: one cosmo binary runs on Linux and macOS hosts
// while runtime.GOOS stays "cosmo". (On Windows hosts a fat APE runs its
// embedded native GOOS=windows payload, so runtime.GOOS is already correct
// there.) Callers that pick host-specific resources — go.dev toolchain
// archives, homebrew paths, CodeQL platform dirs, host binary names — must
// use hostos.GOOS() instead of runtime.GOOS. runtime.GOARCH needs no such
// wrapper: a fat APE runs the payload matching the host architecture.
package hostos

import "runtime"

// GOOS returns runtime.GOOS; compiled-for and host OS are identical for every non-cosmo build.
func GOOS() string { return runtime.GOOS }

// Detect reports the host OS and how it was determined. For a non-cosmo build
// there is nothing to determine — the compiler already knew.
func Detect() Detection {
	return Detection{OS: runtime.GOOS, Method: "compiled"}
}
