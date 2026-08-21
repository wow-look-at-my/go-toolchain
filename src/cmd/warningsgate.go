package cmd

import (
	"fmt"
	"strings"

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
//
// The failure RE-PRINTS every warning it is failing on. A bare count sends
// the reader hunting back through the log and inviting a guess at which
// output was to blame — and the loudest lines in a build log (the watchdog's
// STALLED banner, say) are usually not the counted ones, because only
// src/logger's Warn/WarnFile increment the counter.
func checkWarningsGate() error {
	n := logger.WarnCount()
	if n <= maxWarnings {
		return nil
	}
	recap := warningsRecap(n, logger.EmittedWarnings())
	if jsonOutput {
		// stdout carries the JSON payload; a block or an annotation there
		// would corrupt it. rawStderr is the documented bypass.
		fmt.Fprintln(rawStderr, recap)
	} else {
		// In GHA this is ONE ::error annotation carrying the whole list
		// (gha.go escapes the newlines, so it annotates intact); locally it
		// is the same block on stderr.
		logError("", recap)
	}
	return fmt.Errorf("build failed: %d warnings emitted (threshold: %d)", n, maxWarnings)
}

// warningsRecap renders the gate failure with every retained warning listed in
// emission order. n is the true count; warnings is what was retained (capped
// at logger.MaxRecordedWarnings), so any difference is reported explicitly
// rather than silently truncated.
func warningsRecap(n int64, warnings []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "build failed: %d warnings emitted (threshold: %d). The warnings, in the order they were emitted:", n, maxWarnings)
	for i, w := range warnings {
		fmt.Fprintf(&b, "\n  %2d. %s", i+1, w)
	}
	if dropped := n - int64(len(warnings)); dropped > 0 {
		fmt.Fprintf(&b, "\n  ... and %d more (only the first %d are recorded)", dropped, logger.MaxRecordedWarnings)
	}
	return b.String()
}
