package cmd

import (
	"fmt"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// maxWarnings is the pipeline's warning budget: a run that emits more than
// this many Warn-level messages through src/logger fails after the pipeline
// completes. Deliberately a constant — there is no flag or environment
// variable to change it.
const maxWarnings = 15

// checkWarningsGate fails the build when the run emitted more than
// maxWarnings warnings. It runs at the END of the pipeline commands (the
// default root run and matrix), after all phases have completed, so the user
// still sees every warning before the failure. Non-pipeline subcommands
// (version, install, cacheprog, ...) are deliberately not gated — the
// cacheprog subprocess is a separate process and never reaches this check.
func checkWarningsGate() error {
	n := logger.WarnCount()
	if n <= maxWarnings {
		return nil
	}
	msg := fmt.Sprintf("build failed: %d warnings emitted (threshold: %d)", n, maxWarnings)
	// Annotate in GHA (same pattern as the coverage gate); skip in --json
	// mode, where an annotation on stdout would corrupt the JSON payload.
	if isGHA() && !jsonOutput {
		logError("", msg)
	}
	return fmt.Errorf("%s", msg)
}
