//go:build linux && !cosmo

package cmd

// hostSpecificInspect never handles anything in a GOOS=linux build: such a
// binary only ever runs on a Linux host, where claudeguard_proc.go's /proc
// classifier is the right one. The cosmo build is the one that has to decide
// at runtime, because one APE runs on both Linux and macOS.
func hostSpecificInspect(uintptr) (sink outputSink, ok, handled bool) {
	return outputSink{}, false, false
}
