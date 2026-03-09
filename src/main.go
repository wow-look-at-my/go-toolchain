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

	// Use proxy.pazer.ai as module/toolchain proxy with direct fallback.
	// This ensures GOTOOLCHAIN=auto can download toolchains even when
	// dl.google.com is unreachable (e.g., DNS failures in CI).
	os.Setenv("GOPROXY", "https://proxy.pazer.ai,direct")
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
