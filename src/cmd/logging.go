package cmd

import (
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// rawStderr bypasses the logger for mid-line progress and prompts the auto-newline/filtering would corrupt.
var rawStderr io.Writer = os.Stderr

// rawStdout is rawStderr's stdout counterpart, for mid-line interactive prompts (unignore confirmation).
var rawStdout io.Writer = os.Stdout

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

// initLogging installs the global default logger at the resolved level, first
// in the root PersistentPreRunE. cacheprog never gets a stdout-capable logger:
// its stdout is the GOCACHEPROG protocol pipe, and a GHA annotation there
// would corrupt the JSON stream.
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
