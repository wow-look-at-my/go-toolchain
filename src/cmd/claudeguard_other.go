//go:build !linux && !cosmo && !darwin

package cmd

// No /proc, so the guard cannot classify stdout and never fires here.
func inspectStdout() outputSink { return outputSink{kind: sinkVisible} }

// isTerminal and pipePeerName exist only so claudeguard_test.go compiles on every platform.
func isTerminal(fd uintptr) bool { return false }

func pipePeerName(string) (comm string, pid int, ok bool) {
	return "", 0, false
}
