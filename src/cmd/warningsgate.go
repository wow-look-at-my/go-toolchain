package cmd

import (
	"fmt"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// maxWarnings is the pipeline's warning budget: a run that emits more than
// this many DISTINCT Warn-level messages through src/logger fails after the
// pipeline completes. Deliberately a constant — there is no flag or
// environment variable to change it.
//
// The budget counts distinct messages because one root cause repeats once per
// file, per module or per retry. Counting each repeat spends the budget on one
// problem and hides every other warning in the run. src/logger folds the
// repeats; see docs/WARNINGS-GATE.md.
const maxWarnings = 15

// checkWarningsGate fails the build when the run emitted more than
// maxWarnings distinct warnings. It runs at the END of the pipeline commands
// (the default root run and matrix), after all phases have completed, so the
// user still sees every warning before the failure. Non-pipeline subcommands
// (version, install, cacheprog, ...) are deliberately not gated — the
// cacheprog subprocess is a separate process and never reaches this check.
//
// The failure RE-PRINTS every warning it is failing on, with the repeat count
// of each. A bare count sends the reader hunting back through the log and
// inviting a guess at which output was to blame — and the loudest lines in a
// build log (the watchdog's STALLED banner, say) are usually not the counted
// ones, because only src/logger's Warn/WarnFile reach the counter.
func checkWarningsGate() error {
	n := logger.WarnCount()
	if n <= maxWarnings {
		return nil
	}
	recap := warningsRecap(n, logger.TotalWarnCount(), logger.EmittedWarnings())
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
