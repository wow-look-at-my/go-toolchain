//go:build !cosmo

// Package hostos reports the operating system of the HOST the binary is running on, as opposed to runtime.GOOS.
package hostos

import "runtime"

// GOOS returns runtime.GOOS; compiled-for and host OS are identical for every non-cosmo build.
func GOOS() string { return runtime.GOOS }

// Detect reports the host OS and how it was determined. For a non-cosmo build
// there is nothing to determine — the compiler already knew.
func Detect() Detection {
	return Detection{OS: runtime.GOOS, Method: "compiled"}
}
