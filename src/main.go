package main

import (
	"os"

	"github.com/wow-look-at-my/go-toolchain/src/cmd"
	"github.com/wow-look-at-my/go-toolchain/src/logx"
)

func init() {
	// When invoked as GOCACHEPROG, skip all env setup — just serve the protocol.
	if isCacheProgInvocation() {
		return
	}
	if _, linked := cmd.LinkedGoArgs(os.Args); linked {
		// A go command the pipeline started takes the environment as it is.
		if cmd.PipelineStartedGo() {
			return
		}
		// One started by hand has nobody to inherit from. It gets the proxy
		// the pipeline uses, and it fetches no other toolchain: this
		// executable is the only one, so a stock Go must not answer instead.
		os.Setenv("GOTOOLCHAIN", "local")
		configureGoEnv()
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
	// This binary is the go command: a child that starts go by name, or the
	// pipeline starting itself under the go subcommand, lands here.
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
