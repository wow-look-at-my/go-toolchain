//go:build linux && !cosmo

package cmd

// Always a no-op on GOOS=linux: the binary only runs on Linux, where the /proc classifier already applies.
func hostSpecificInspect(uintptr) (sink outputSink, ok, handled bool) {
	return outputSink{}, false, false
}
