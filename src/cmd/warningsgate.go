package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// maxWarnings is the pipeline's DISTINCT-warning budget. see docs/WARNINGS-GATE.md
const maxWarnings = 15

// warningsUncappedUntil lifts the budget while CI reports comment repairs
// instead of applying them, which surfaces a backlog no run has paid down. The
// date is the mechanism, not a comment: past it the budget is maxWarnings
// again with no edit and no way to forget.
var warningsUncappedUntil = time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)

// warningsBudget answers the budget in force now.
func warningsBudget() int64 {
	if time.Now().UTC().Before(warningsUncappedUntil) {
		return 1 << 30
	}
	return maxWarnings
}

// checkWarningsGate fails the build when the run emitted more than
// maxWarnings distinct warnings. It runs at the END of the pipeline commands,
// after every phase has printed, so the user sees all warnings before the
// failure. Non-pipeline subcommands are not gated. The failure re-prints
// every warning with its repeat count, since a bare count sends the reader
// hunting back through the log for which output was to blame.
func checkWarningsGate() error {
	n := logger.WarnCount()
	if n <= warningsBudget() {
		return nil
	}
	recap := warningsRecap(n, logger.TotalWarnCount(), logger.EmittedWarnings())
	if jsonOutput {
		// stdout carries the JSON payload; rawStderr is the documented bypass.
		fmt.Fprintln(rawStderr, recap)
	} else {
		// In GHA this is a single ::error annotation carrying the whole list.
		logError("", recap)
	}
	return fmt.Errorf("build failed: %d distinct warnings emitted (threshold: %d)", n, maxWarnings)
}

// warningsRecap renders the gate failure with every retained warning listed in
// emission order. distinct is the true distinct count and what the gate
// fails on; total is every emission, reported when the counts differ so a folded
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
