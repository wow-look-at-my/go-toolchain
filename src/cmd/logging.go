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
// Precedence: --verbose > a set GOCACHE_DEBUG (maps to debug) > default info.
func resolveLogLevel() logger.Level {
	if verbose {
		return logger.LevelDebug
	}
	if os.Getenv("GOCACHE_DEBUG") == "1" {
		return logger.LevelDebug
	}
	return logger.LevelInfo
}

// initLogging installs the global logger before the rest of the root PersistentPreRunE.
// cacheprog gets a stderr-only logger: its stdout is the GOCACHEPROG pipe,
// which a GHA annotation would corrupt.
func initLogging(cmd *cobra.Command) error {
	level := resolveLogLevel()
	if isCacheProg(cmd) {
		logger.InitSubprocess(level)
		return nil
	}
	logger.Init(logger.Options{Level: level, GHAAuto: true})
	return nil
}
