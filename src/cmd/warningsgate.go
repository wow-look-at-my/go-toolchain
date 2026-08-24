package cmd

import (
	"fmt"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// maxWarnings is the pipeline's DISTINCT-warning budget; a constant on purpose. see docs/WARNINGS-GATE.md
const maxWarnings = 15

// checkWarningsGate fails the build when the run emitted more than
// maxWarnings distinct warnings. It runs at the END of the pipeline commands,
// after every phase has printed, so the user sees all warnings before the
// failure. Non-pipeline subcommands are not gated. The failure re-prints
// every warning with its repeat count, since a bare count sends the reader
// hunting back through the log for which output was to blame.
func checkWarningsGate() error {
	n := logger.WarnCount()
	if n <= maxWarnings {
		return nil
	}
	recap := warningsRecap(n, logger.TotalWarnCount(), logger.EmittedWarnings())
	if jsonOutput {
		// stdout carries the JSON payload; rawStderr is the documented bypass.
		fmt.Fprintln(rawStderr, recap)
	} else {
		// In GHA this is one ::error annotation carrying the whole list.
		logError("", recap)
	}
	return fmt.Errorf("build failed: %d distinct warnings emitted (threshold: %d)", n, maxWarnings)
}

// warningsRecap renders the gate failure with every retained warning listed in
// first-emission order. distinct is the true distinct count and what the gate
// fails on; total is every emission, reported when the two differ so a folded
// repeat is visible rather than hidden. warnings is what was retained (capped
// at logger.MaxRecordedWarnings), so any difference is reported explicitly
// rather than silently truncated.
func warningsRecap(distinct, total int64, warnings []logger.Warning) string {
	var b strings.Builder
	fmt.Fprintf(&b, "build failed: %d distinct warnings emitted (threshold: %d)", distinct, maxWarnings)
	if total > distinct {
		fmt.Fprintf(&b, ", %d emitted in total (a repeat counts once)", total)
	}
	fmt.Fprint(&b, ". The warnings, in the order they were first emitted:")
	for i, w := range warnings {
		fmt.Fprintf(&b, "\n  %2d. %s", i+1, w.Message)
		if w.Count > 1 {
			fmt.Fprintf(&b, " (emitted %d times)", w.Count)
		}
	}
	if dropped := distinct - int64(len(warnings)); dropped > 0 {
		fmt.Fprintf(&b, "\n  ... and %d more (only the first %d are recorded)", dropped, logger.MaxRecordedWarnings)
	}
	return b.String()
}
