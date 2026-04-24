package main

import (
	"os"

	"github.com/wow-look-at-my/go-toolchain/src/cmd"
	"github.com/wow-look-at-my/go-toolchain/src/logx"
)

func init() {
	// When invoked as GOCACHEPROG, stdout must carry raw JSON protocol
	// traffic to the Go toolchain — prepending timestamps would corrupt it.
	// Skip log-timestamp installation and all env setup in that mode.
	if isCacheProgInvocation() {
		return
	}

	// Swap os.Stdout / os.Stderr for pipes so every line emitted by this
	// process (and by inherited subprocess FDs) is prefixed with a
	// wall-clock timestamp. Must happen before any other code writes.
	logx.Install()

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
	os.Exit(run())
}

func run() int {
	// Drain any buffered pipe content before exit so nothing is lost.
	defer logx.Flush()
	if needsGo() {
		if err := cmd.EnsureGoVersion(); err != nil {
			logx.Logf("go bootstrap: %v", err)
			return 1
		}
	}
	if err := cmd.Execute(); err != nil {
		return 1
	}
	return 0
}
