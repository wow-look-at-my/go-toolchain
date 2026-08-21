//go:build cosmo

package cmd

import "github.com/wow-look-at-my/go-toolchain/src/hostos"

// hostSpecificInspect classifies fd when the HOST needs a classifier other
// than claudeguard_proc.go's /proc one. handled is false when the /proc path
// is the right answer.
//
// A cosmo APE runs on both Linux and macOS from one binary, so this is decided
// at runtime, on hostos.GOOS() -- runtime.GOOS is "cosmo" everywhere and says
// nothing about where the process actually is. Host detection is asserted on
// every CI run, inside dats' sandbox and outside it, precisely because
// everything below hangs off it.
//
// ok is false when the darwin classifier could not run a probe it needed. The
// caller must treat that as a blind classifier -- announced, never a silent
// pass -- rather than acting on a partial answer.
func hostSpecificInspect(fd uintptr) (sink outputSink, ok, handled bool) {
	if hostos.GOOS() != "darwin" {
		return outputSink{}, false, false
	}
	sink, ok = inspectFDDarwinHost(fd)
	return sink, ok, true
}
