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

	// Route module/toolchain downloads through proxy.pazer.ai which proxies
	// proxy.golang.org and serves content directly (no redirects to Google
	// storage), avoiding DNS failures when dl.google.com is unreachable.
	os.Setenv("GOPROXY", "https://proxy.pazer.ai/proxy.golang.org|direct")
	os.Setenv("GONOSUMDB", "*")
	os.Setenv("GONOSUMCHECK", "*")
}

func main() {
	if err := cmd.EnsureGoVersion(); err != nil {
		fmt.Fprintf(os.Stderr, "go bootstrap: %v\n", err)
		os.Exit(1)
	}
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
