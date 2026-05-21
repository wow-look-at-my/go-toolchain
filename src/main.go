package main

import (
	"fmt"
	"os"

	"github.com/wow-look-at-my/go-toolchain/src/cmd"
)

// Build-time variables, set via -ldflags -X targeting this main package.
var (
	buildVersion   = "dev"
	buildCommit    = "unknown"
	buildTimestamp = ""
	buildDate      = ""
)

func init() {
	// When invoked as GOCACHEPROG, skip all env setup — just serve the protocol.
	if isCacheProgInvocation() {
		return
	}

	// Let Go automatically download the correct toolchain when go.mod
	// requires a newer version than the one installed.
	os.Setenv("GOTOOLCHAIN", "auto")

	// Configure Go module proxy and checksum database settings,
	// respecting user-configured proxies and sumdb (e.g. pazer.io).
	configureGoEnv()

	// Clear NO_PROXY so that all traffic (including *.google.com and
	// *.googleapis.com) routes through the environment's egress proxy,
	// which handles DNS resolution. Without this, Go tries to reach
	// Google domains directly but DNS cannot resolve them.
	os.Setenv("NO_PROXY", "")
	os.Setenv("no_proxy", "")
}

func isCacheProgInvocation() bool {
	for _, arg := range os.Args[1:] {
		if arg == "cacheprog" {
			return true
		}
		if arg == "--" {
			return false
		}
	}
	return false
}

func needsGo() bool {
	for _, arg := range os.Args[1:] {
		if arg == "--" {
			return true
		}
		switch arg {
		case "version", "cacheprog":
			return false
		}
	}
	return true
}

func main() {
	cmd.SetBuildInfo(buildVersion, buildCommit, buildTimestamp, buildDate)
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
