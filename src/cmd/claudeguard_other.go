//go:build !linux && !cosmo && !darwin

package cmd

// inspectStdout cannot classify stdout without /proc, so the output guard never fires here (windows and other platforms).
func inspectStdout() outputSink { return outputSink{kind: sinkVisible} }

// isTerminal and pipePeerName exist only so claudeguard_test.go compiles on every platform.
func isTerminal(fd uintptr) bool { return false }

func pipePeerName(string) (comm string, pid int, ok bool) {
	return "", 0, false
}
