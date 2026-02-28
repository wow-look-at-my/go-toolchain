package main

import (
	"os"

	"github.com/wow-look-at-my/go-toolchain/src/cmd"
)

func init() {
	// Disable Go's phone-home behavior - bypass proxy and checksum database
	os.Setenv("GOPROXY", "direct")
	os.Setenv("GOSUMDB", "off")
	os.Setenv("GONOSUMCHECK", "*")
}

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
