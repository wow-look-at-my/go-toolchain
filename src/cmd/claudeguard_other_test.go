//go:build !linux && !cosmo && !darwin

package cmd

// Stubs so claudeguard_test.go compiles where claudeguard_proc.go cannot.

func isTerminal(fd uintptr) bool { return false }

func pipePeerName(string) (comm string, pid int, ok bool) {
	return "", 0, false
}
