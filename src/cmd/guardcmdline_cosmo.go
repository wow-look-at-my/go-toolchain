//go:build cosmo

package cmd

import "github.com/wow-look-at-my/go-toolchain/src/hostos"

// A fat APE boots on either host, so it chooses its argv read at run time:
// /proc on linux, ps on darwin. runtime.GOOS answers "cosmo" on each,
// which is no answer at all -- an APE on a Mac asking /proc reads nothing, and
// the guard then acquits every captured run it was built to refuse.
func readCmdline(pid int) ([]string, bool) {
	if hostos.GOOS() == "darwin" {
		return readCmdlinePS(pid)
	}
	return readCmdlineProc(pid)
}
