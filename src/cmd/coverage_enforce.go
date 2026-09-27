package cmd

import (
	"fmt"
	"math"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	gotest "github.com/wow-look-at-my/go-toolchain/src/test"
)

// enforceCoverage compares the measured total coverage against the effective
// minimum and returns nil (pass) or an error (fail). It panics only when the
// coverage data itself is broken: tests ran over code with coverable
// statements, yet the profile recorded nothing.
//
// The "no uncovered statements while below the minimum" corner (an empty
// coverage profile) splits several ways:
//   - The module has no coverable statements at all — e.g. an embed-only or
//     declarations-only module. There is nothing to measure, so an empty
//     profile is vacuously complete: pass with a note instead of panicking.
//   - The module has coverable statements but produced no test results (no
//     *_test.go files, so `go test` reported "[no test files]" everywhere):
//     fail with an actionable message instead of a panic.
//   - Tests ran and coverable statements exist, yet no statements were
//     measured: the profile was lost or corrupted (e.g. clobbered mid-run) —
//     a broken setup that must not be silently allowed. Panic.
func enforceCoverage(report *gotest.Report, result *gotest.TestResult, effectiveMin float32, quiet bool) error {
	// Round to the display's precision for comparison
	roundedTotal := float32(math.Round(float64(report.Total)*10) / 10)
	roundedMin := float32(math.Round(float64(effectiveMin)*10) / 10)
	if roundedTotal >= roundedMin {
		return nil
	}

	// Calculate total uncovered statements across all packages
	var totalUncovered int
	for _, pkg := range report.Packages {
		totalUncovered += pkg.Uncovered()
	}

	// Packages exist but no statements were measured.
	if totalUncovered == 0 && len(report.Packages) > 0 {
		if !gotest.HasCoverableStatements(".") {
			// Nothing to measure here (embed-only or declarations-only module); an empty profile is complete, not broken.
			if !quiet {
				logger.Info("⇒ No coverable statements in this module — nothing to measure, skipping coverage check")
			}
			return nil
		}
		if len(result.TestCases) == 0 {
			msg := fmt.Sprintf("coverage %.1f%% is below minimum %.1f%%: module has coverable statements but no test results — add *_test.go files with Test functions", report.Total, effectiveMin)
			if isGHA() && !quiet {
				logError("", msg)
			}
			return fmt.Errorf("%s", msg)
		}
		// Tests ran over coverable code, yet nothing was measured -- the coverage profile is missing or broken.
		panic(fmt.Sprintf("coverage %.1f%% is below minimum %.1f%% with 0 uncovered statements — coverage data is missing or broken", report.Total, effectiveMin))
	}

	// Allow reduced coverage while every file stays under the uncovered-statement bar below (small files can't easily reach the minimum).
	allSmall := true
	for _, pkg := range report.Packages {
		for _, f := range pkg.Files {
			if f.Uncovered() >= 10 {
				allSmall = false
				break
			}
		}
		if !allSmall {
			break
		}
	}
	if allSmall {
		if !quiet {
			logger.Info("⇒ Coverage %.1f%% is below minimum %.1f%%, but no file has 10+ uncovered statements — allowing", report.Total, effectiveMin)
		}
		return nil
	}

	msg := fmt.Sprintf("coverage %.1f%% is below minimum %.1f%%", report.Total, effectiveMin)
	// Skip annotation in --json mode: a workflow command on stdout would corrupt the already-written JSON payload.
	if isGHA() && !quiet {
		logError("", msg)
	}
	return fmt.Errorf("%s", msg)
}
