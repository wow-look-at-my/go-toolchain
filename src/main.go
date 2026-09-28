package main

import (
	"os"

	"github.com/wow-look-at-my/go-toolchain/src/cmd"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/logx"
)

func init() {
	// When invoked as GOCACHEPROG, skip all env setup — just serve the protocol.
	if isCacheProgInvocation() {
		return
	}
	// The go command and its tools take the environment as it is, because the
	// command that started them configured it. A caller that starts this
	// binary as go itself has no such parent, and a runner hands it GOPROXY
	// set to the empty value, which the go command reads as a proxy list with
	// no entries and refuses. An environment naming a proxy is still taken as
	// it is: only the absence is filled.
	if _, linked := cmd.LinkedGoArgs(os.Args); linked {
		if os.Getenv("GOPROXY") == "" {
			configureGoEnv()
		}
		return
	}

	// Let Go auto-download the toolchain go.mod requires.
	os.Setenv("GOTOOLCHAIN", "auto")

	// Configure the Go module proxy and sumdb, honoring user config.
	configureGoEnv()

	// Clear NO_PROXY: Google domains must route through the egress proxy for DNS.
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

func main() {
	// Ahead of every mode, the linked go command included, so a local run cannot inherit a CI pin.
	if err := cmd.CheckCIOnlyEnv(); err != nil {
		logger.Error("go-toolchain: %v", err)
		os.Exit(1)
	}

	// This binary is the go command when it runs under the name go, and a
	// linked tool when the go command starts it as "tool <name>".
	if code, linked := cmd.RunLinkedGo(os.Args); linked {
		os.Exit(code)
	}

	// Install the elapsed-duration pipeline. Skip it for GOCACHEPROG: its
	// stdout is a JSON protocol pipe that must stay undecorated.
	if !isCacheProgInvocation() {
		logx.Install()
	}

	// Check for a newer go-toolchain in the background; ReportUpdateCheck
	// surfaces or kills it on every exit path, so it never blocks.
	if shouldCheckForUpdate() {
		cmd.StartUpdateCheck()
	}

	// The toolchain resolves inside the root command, after cobra knows which command runs -- see skipToolchain.
	err := cmd.Execute()
	cmd.ReportUpdateCheck()
	logx.Flush()
	if err != nil {
		logger.Error("go-toolchain: %v", err)
		os.Exit(1)
	}
}

// shouldCheckForUpdate skips the GOCACHEPROG subprocess and `version`, which
// already reports its own staleness.
func shouldCheckForUpdate() bool {
	if isCacheProgInvocation() {
		return false
	}
	if _, linked := cmd.LinkedGoArgs(os.Args); linked {
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
