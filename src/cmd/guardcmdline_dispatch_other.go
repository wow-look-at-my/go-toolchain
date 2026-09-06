//go:build !darwin && !cosmo

package cmd

// A host whose GOOS is what the binary was built for reads /proc directly.
func readCmdline(pid int) ([]string, bool) { return procCmdline(pid) }
