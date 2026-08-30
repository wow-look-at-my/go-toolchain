//go:build !linux && !cosmo && !darwin

package cmd

// inspectStdout cannot classify stdout without /proc, so the output guard never
// fires here. Nothing this org ships reaches this file: the APE is a cosmo build
// on every host, NT included, so a Windows run takes claudeguard_proc.go's blind
// path instead. This covers a consumer building go-toolchain with a stock Go.
func inspectStdout() outputSink { return outputSink{kind: sinkVisible} }

// isTerminal and pipePeerName exist only so claudeguard_test.go compiles on every platform.
func isTerminal(fd uintptr) bool { return false }

func pipePeerName(string) (comm string, pid int, ok bool) {
	return "", 0, false
}
