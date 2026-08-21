//go:build !linux && !cosmo && !darwin

package cmd

// inspectStdout cannot classify the stdout descriptor without /proc, so the
// output guard never fires on the remaining platforms (windows and anything
// else). (The released "linux" binaries are GOOS=cosmo APE copies and use the
// real classifier in claudeguard_proc.go, and darwin has its own real
// classifier in claudeguard_darwin.go — this stub must never shadow either.)
func inspectStdout() outputSink { return outputSink{kind: sinkVisible} }

// isTerminal and pipePeerName exist only so claudeguard_test.go compiles on
// every platform; the tests that call them skip themselves outside linux.
func isTerminal(fd uintptr) bool { return false }

func pipePeerName(string) (comm string, pid int, ok bool) {
	return "", 0, false
}
