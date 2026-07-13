package cmd

import (
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// rawStderr is a deliberate logger bypass for output that MUST stay on the raw
// stderr stream: mid-line progress fragments that are printed without a
// trailing newline and completed later on the same line (bootstrap download /
// extract timings), and interactive prompts that await input on the same line
// (release confirmation). The logger's auto-newline and level filtering would
// corrupt or hide these. Held in a variable, which the bannedoutput analyzer
// deliberately permits.
var rawStderr io.Writer = os.Stderr

// isCacheProg reports whether cmd or any of its ancestors is the cacheprog
// subcommand (same ancestor walk as skipCache).
func isCacheProg(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Name() == "cacheprog" {
			return true
		}
	}
	return false
}

// resolveLogLevel resolves the effective log level for this invocation.
// Precedence: explicit --log-level > --verbose > GOCACHE_DEBUG=1 (maps to
// debug) > default info. An unknown --log-level value is a returned error.
func resolveLogLevel(cmd *cobra.Command) (logger.Level, error) {
	if f := cmd.Flags().Lookup("log-level"); f != nil && f.Changed {
		return logger.ParseLevel(logLevel)
	}
	if verbose {
		return logger.LevelDebug, nil
	}
	if os.Getenv("GOCACHE_DEBUG") == "1" {
		return logger.LevelDebug, nil
	}
	return logger.LevelInfo, nil
}

// initLogging installs the global default logger at the resolved level. It
// runs first in the root PersistentPreRunE, for every command.
//
// The cacheprog subprocess must never get a stdout-capable logger: its stdout
// is the GOCACHEPROG protocol pipe cmd/go parses, and a GHA "::warning"
// annotation there (the logger writes annotations to stdout when
// GITHUB_ACTIONS=true) corrupts the JSON stream. runCacheProg re-initializes
// the same way as its first action; the special case here closes the window
// between this pre-run and that re-init.
func initLogging(cmd *cobra.Command) error {
	level, err := resolveLogLevel(cmd)
	if err != nil {
		return err
	}
	if isCacheProg(cmd) {
		logger.InitSubprocess(level)
		return nil
	}
	logger.Init(logger.Options{Level: level, GHAAuto: true})
	return nil
}
