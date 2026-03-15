package main

import (
	"fmt"
	"os"

	"github.com/wow-look-at-my/go-toolchain/src/cmd"
)

func init() {
	// Let Go automatically download the correct toolchain when go.mod
	// requires a newer version than the one installed.
	os.Setenv("GOTOOLCHAIN", "auto")

	// Disable Go's phone-home behavior - bypass proxy and checksum database.
	// Use GONOSUMDB instead of GOSUMDB=off so toolchain auto-downloads still work.
	os.Setenv("GOPROXY", "direct")
	os.Setenv("GONOSUMDB", "*")
	os.Setenv("GONOSUMCHECK", "*")

	// Clear NO_PROXY so that all traffic (including *.google.com and
	// *.googleapis.com) routes through the environment's egress proxy,
	// which handles DNS resolution. Without this, Go tries to reach
	// Google domains directly but DNS cannot resolve them.
	os.Setenv("NO_PROXY", "")
	os.Setenv("no_proxy", "")
}

func needsGo() bool {
	for _, arg := range os.Args[1:] {
		if arg == "--" {
			return true
		}
		if arg == "version" {
			return false
		}
	}
	return true
}

func main() {
	if needsGo() {
		if err := cmd.EnsureGoVersion(); err != nil {
			fmt.Fprintf(os.Stderr, "go bootstrap: %v\n", err)
			os.Exit(1)
		}
	}
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
