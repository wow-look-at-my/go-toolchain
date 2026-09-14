//go:build !darwin && !cosmo

package cmd

// A host with a /proc reads argv from it. The host reaching this file without
// one is windows, which gets no classifier at all (claudeguard_other.go), so
// the read failing there costs nothing.
func readCmdline(pid int) ([]string, bool) { return readCmdlineProc(pid) }
