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

	// Kick off a non-blocking background check for a newer go-toolchain on
	// buildhost. It runs while the real work happens and is surfaced (or killed)
	// by ReportUpdateCheck on every exit path — it never blocks or delays.
	if shouldCheckForUpdate() {
		cmd.StartUpdateCheck()
	}

	if needsGo() {
		if err := cmd.EnsureGoVersion(); err != nil {
			logx.Logf("go bootstrap: %v", err)
			cmd.ReportUpdateCheck()
			return 1
		}
	}
	err := cmd.Execute()
	cmd.ReportUpdateCheck()
	if err != nil {
		return 1
	}
	return 0
}

// shouldCheckForUpdate reports whether to start the background update check. It
// is skipped for the GOCACHEPROG subprocess (spawned by the Go build itself, not
// a user invocation) and for the `version` command, which already reports its
// own staleness.
func shouldCheckForUpdate() bool {
	if isCacheProgInvocation() {
		return false
	}
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
