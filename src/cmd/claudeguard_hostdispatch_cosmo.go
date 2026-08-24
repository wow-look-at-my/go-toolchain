//go:build cosmo

package cmd

import "github.com/wow-look-at-my/go-toolchain/src/hostos"

// hostSpecificInspect classifies fd on a non-/proc host, via hostos.GOOS().
// ok is false for a blind classifier; caller must not act on a partial answer.
func hostSpecificInspect(fd uintptr) (sink outputSink, ok, handled bool) {
	if hostos.GOOS() != "darwin" {
		return outputSink{}, false, false
	}
	sink, ok = inspectFDDarwinHost(fd)
	return sink, ok, true
}
