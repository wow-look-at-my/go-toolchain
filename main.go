package main

import (
	"os"

	"github.com/wow-look-at-my/go-toolchain/src/cmd"
)

func init() {
	// Disable Go's phone-home behavior - bypass proxy and checksum database.
	// Use GONOSUMDB instead of GOSUMDB=off so toolchain auto-downloads still work.
	os.Setenv("GOPROXY", "direct")
	os.Setenv("GONOSUMDB", "*")
	os.Setenv("GONOSUMCHECK", "*")
}

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
