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

// resolveLogLevel resolves the effective log level for this invocation.
// Precedence: --verbose > default info.
func resolveLogLevel() logger.Level {
	if verbose {
		return logger.LevelDebug
	}
	return logger.LevelInfo
}

// initLogging installs the global logger before the rest of the root PersistentPreRunE.
func initLogging(cmd *cobra.Command) error {
	logger.Init(logger.Options{Level: resolveLogLevel(), GHAAuto: true})
	return nil
}
