//go:build cosmo

// A fat APE reports GOOS=cosmo on every host, so the reader dispatches on the
// HOST. The sysctl reader ships only in a native darwin build.

package cmd

import "github.com/wow-look-at-my/go-toolchain/src/hostos"

func readCmdline(pid int) ([]string, bool) {
	if hostos.GOOS() == "darwin" {
		return psCmdline(pid)
	}
	return procCmdline(pid)
}
