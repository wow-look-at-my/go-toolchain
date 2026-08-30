package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/lint"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	gotest "github.com/wow-look-at-my/go-toolchain/src/test"
	gotrace "github.com/wow-look-at-my/go-toolchain/src/trace"
	"github.com/wow-look-at-my/go-toolchain/src/vet"
)

// vetRunFunc is the vet phase, as a seam. Why: docs/CI.md.
var vetRunFunc = vet.RunWithProgress

// RunTestsWithCoverage runs go mod tidy, go vet, tests with coverage, and
// checks coverage against the threshold. Used by both the default command
// and the matrix command.
// Returns (filesChanged, testResult, error) where filesChanged indicates if vet applied any fixes.
func RunTestsWithCoverage(r runner.CommandRunner, quiet bool) (bool, *gotest.TestResult, error) {
	// Fix any placeholder-version dependencies before go mod tidy
	if err := FixBogusDepsVersions(r); err != nil {
		return false, nil, err
	}

	// An org dependency carrying a plain version pin gets the branch marker
	// added up front, so the re-resolution below owns it from this run on.
	if _, err := EnforceOrgBranchTracking(r); err != nil {
		return false, nil, err
	}

	// Re-resolve any dependency pinned to follow a branch (see depsbranch.go)
	if _, err := UpdateTrackedBranchDeps(r); err != nil {
		return false, nil, err
	}

	// Handle vanity-URL modules: inject replace directives for unreachable hosts
	vanity, vanityErr := injectVanityReplaces()
	if vanityErr != nil {
		return false, nil, fmt.Errorf("vanity URL handling failed: %w", vanityErr)
	}
	// Removes the injected vanity replaces on every return path.
	defer func() {
		_ = removeVanityReplaces(vanity)
	}()

	if err := runModTidy(r, quiet); err != nil {
		return false, nil, err
	}

	if needsGenerate() {
		var genStep *step
		if !quiet {
			genStep = logStep("go generate ./...")
		}
		if err := runGenerate(quiet, generateHash); err != nil {
			return false, nil, fmt.Errorf("go generate failed: %w", err)
		}
		if genStep != nil {
			genStep.noteOutput() // generate always prints directives
			genStep.done()
		}
		// Run tidy again after generate in case new imports were added
		var tidyStep2 *step
		if !quiet {
			tidyStep2 = logStep("go mod tidy (post-generate)")
		}
		proc, err := runner.Cmd("go", "mod", "tidy", "-v").WithOnFirstOutput(func() {
			if tidyStep2 != nil {
				tidyStep2.noteOutput()
			}
		}).Run(r)
		if err != nil {
			return false, nil, fmt.Errorf("go mod tidy failed: %w", err)
		}
		if err := proc.Wait(); err != nil {
			return false, nil, fmt.Errorf("go mod tidy failed: %w", err)
		}
		if tidyStep2 != nil {
			tidyStep2.done()
		}
	}

	var vetStep *step
	if !quiet {
		vetStep = logStep("go vet ./...")
	}
	var vetPhaseStep *step
	vetProgress := func(phase string) {
		if quiet {
			return
		}
		if vetPhaseStep != nil {
			vetPhaseStep.done()
		} else if vetStep != nil {
			vetStep.noteOutput()
		}
		vetPhaseStep = logSubStep("vet: "+phase, "main")
	}
	// On CI (CI=true) fixers run check-only: any change (gofmt, import migration, testify cast) is a hard error, not an auto-fix.
	fix := os.Getenv("CI") == ""
	filesChanged, err := vetRunFunc(fix, vetProgress)
	if err != nil {
		// If in-process vet fails due to a Go version mismatch (a binary built
		// with an older Go than the project requires), fall back to external go vet
		// which uses the bootstrapped Go version.
		if strings.Contains(err.Error(), "package requires newer Go version") {
			if vetPhaseStep != nil {
				vetPhaseStep.done()
				vetPhaseStep = nil
			}
			vetPhaseStep = logSubStep("vet: fallback to external go vet", "main")
			cmd := exec.Command("go", "vet", "./...")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if extErr := cmd.Run(); extErr != nil {
				return false, nil, fmt.Errorf("go vet failed: %w", extErr)
			}
			filesChanged = false
			err = nil
		} else if isUnreadableExportData(err) {
			// A dependency's compiled API did not decode, which says nothing about this source.
			disableSharedBuildCache()
			logger.Warn("⇒ Warning: vet could not read the compiler's export data (%s) for %s -- that is a dependency's compiled API, not your source. Retrying with every dependency type-checked from source, and with the shared build cache (GOCACHEPROG) off for the rest of this run. A damaged cache entry and export data newer than this binary's importer both land here.",
				exportDataSignature(err), strings.Join(unreadableExportPackages(err), ", "))
			if vetPhaseStep != nil {
				vetPhaseStep.done()
				vetPhaseStep = nil
			}
			vetPhaseStep = logSubStep("vet: retry against dependency source", "main")
			filesChanged, err = vet.RunFromSource(fix, vetProgress)
			if err != nil {
				if isUnreadableExportData(err) {
					return false, nil, unreadableExportDataError(err)
				}
				return false, nil, fmt.Errorf("vet failed: %w", err)
			}
		} else {
			return false, nil, fmt.Errorf("vet failed: %w", err)
		}
	}
	// Finish the last vet sub-phase
	if vetPhaseStep != nil {
		vetPhaseStep.done()
	}
	if vetStep != nil {
		vetStep.done()
	}

	// Vet is the last file-modifying stage; fail fast here.
	if err := checkDirtyInCIWithVanityRestored(vanity); err != nil {
		return false, nil, err
	}

	printCacheStats(false)

	if dupcode {
		runDuplicateCheck()
	}

	fileLenStep := logSubStep("File length check", "main")
	if err := checkFileLength("."); err != nil {
		return false, nil, err
	}
	fileLenStep.done()

	var testStep *step
	if !quiet {
		testStep = logStep("Running tests with coverage")
	}

	// A process-unique path avoids collisions with mock-runner tests that write and delete this file.
	coverDir := filepath.Join(argListTempDir(hostos.GOOS()), "go-toolchain-cov")
	// Report the mkdir. Dropping it made the test phase fail later on the
	// coverage file instead, which names a missing path and not the reason
	// it is missing.
	if err := os.MkdirAll(coverDir, 0o755); err != nil {
		if testStep != nil {
			testStep.failed()
		}
		return false, nil, fmt.Errorf("coverage directory %s: %w", coverDir, err)
	}
	coverFile := filepath.Join(coverDir, fmt.Sprintf("coverage-%d.out", os.Getpid()))
	defer os.Remove(coverFile)

	var onTestOutput func()
	if testStep != nil {
		onTestOutput = testStep.noteOutput
	}
	result, testErr := gotest.RunTests(r, verbose, coverFile, onTestOutput, GetTimeline())
	if result == nil {
		if testStep != nil {
			testStep.failed()
		}
		return false, nil, fmt.Errorf("tests failed: %w", testErr)
	}
	if testErr != nil && testStep != nil {
		testStep.failed()
	} else if testStep != nil {
		testStep.done()
	}

	// Record per-test events in the trace.
	if activeTrace != nil && result != nil {
		// Only leaf tests are traced; parent durations include children and would overlap.
		hasSubtest := set.New[string]()
		for _, tc := range result.TestCases {
			if i := strings.LastIndex(tc.Test, "/"); i > 0 {
				hasSubtest.Add(tc.Package + "." + tc.Test[:i])
			}
		}
		for _, tc := range result.TestCases {
			if tc.Elapsed <= 0 || tc.End.IsZero() {
				continue
			}
			if hasSubtest.Contains(tc.Package + "." + tc.Test) {
				continue // skip parent, children cover the time
			}
			dur := time.Duration(tc.Elapsed * float64(time.Second))
			pkg := tc.Package
			if idx := strings.LastIndex(pkg, "/"); idx >= 0 {
				pkg = pkg[idx+1:]
			}
			activeTrace.Record(gotrace.Event{
				Name:     tc.Test,
				Category: "test",
				Thread:   "test/" + pkg,
				Start:    tc.End.Add(-dur),
				End:      tc.End,
				Failed:   tc.Status == "fail",
				Args:     map[string]string{"package": tc.Package, "status": tc.Status},
			})
		}
	}

	report := &result.Coverage

	// If tests failed, show failure details and return error (no coverage output)
	if testErr != nil {
		if !quiet && result.FailureOutput != "" {
			logger.Output("\n⇒ Test failures:")
			logger.Output("%s", colorRed+result.FailureOutput+colorReset)
		}
		return false, result, fmt.Errorf("tests failed: %w", testErr)
	}

	if quiet {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "\t")
		if err := enc.Encode(report); err != nil {
			return false, nil, fmt.Errorf("failed to encode JSON: %w", err)
		}
	} else {
		logger.Info("\n⇒ Coverage targets (by potential gain):")
		report.Print()

		logger.Output("\n⇒ Total coverage: %s", colorPct(ColorPct{Pct: report.Total, Format: "%.1f%%"}))
	}

	// Coverage enforcement: the default minimum below, or the watermark's grace floor if lower.
	var effectiveMin float32 = 80.0
	wm, wmExists, wmErr := gotest.GetWatermark(".")
	if wmErr != nil {
		// Watermark read failed (e.g., xattrs not supported) - warn and use default
		if !quiet {
			logger.Warn("⇒ Warning: %v (using default %.0f%%)", wmErr, effectiveMin)
		}
		wmExists = false
	}
	if wmExists {
		grace := wm - 2.5
		if grace < effectiveMin {
			effectiveMin = grace
		}
		if !quiet {
			logger.Info("⇒ Watermark: %.1f%% (effective minimum: %.1f%%)", wm, effectiveMin)
		}
		// Ratchet up: update watermark if coverage improved
		if report.Total > wm {
			if err := gotest.SetWatermark(".", report.Total); err != nil {
				if !quiet {
					logger.Warn("⇒ Warning: failed to update watermark: %v", err)
				}
			} else if !quiet {
				logger.Info("⇒ Watermark updated: %.1f%% -> %.1f%%", wm, report.Total)
			}
		}
	}

	if err := enforceCoverage(report, result, effectiveMin, quiet); err != nil {
		return false, result, err
	}

	return filesChanged, result, nil
}

var errFound = fmt.Errorf("found")

// needsGenerate returns true if any .go file contains a //go:generate directive.
func needsGenerate() bool {
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "//go:generate ") {
				return errFound
			}
		}
		return nil
	})
	return err == errFound
}

// runDuplicateCheck scans Go source files for near-duplicate function bodies
// and prints warnings. It never causes a build failure.
func runDuplicateCheck() {
	if !jsonOutput {
		logger.Info("⇒ Checking for near-duplicate code")
	}

	paths, err := walkGoFiles(".")
	if err != nil || len(paths) == 0 {
		return
	}

	fset := token.NewFileSet()
	allFiles := make(map[string]*ast.File)
	for _, path := range paths {
		f, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			continue
		}
		allFiles[path] = f
	}

	if len(allFiles) == 0 {
		return
	}

	reports := lint.RunOnFiles(allFiles, fset, lintThreshold, lintMinNodes)
	if len(reports) == 0 {
		return
	}

	if jsonOutput {
		return
	}

	logger.Info("\n%s near-duplicate code: found %d pair(s)%s", colorYellow, len(reports), colorReset)
	for i, r := range reports {
		logger.Info("  %d. %.0f%% similar: %s (%s:%d) and %s (%s:%d)",
			i+1, r.Similarity*100,
			r.FuncA, r.FileA, r.LineA,
			r.FuncB, r.FileB, r.LineB,
		)
		if verbose {
			logger.Info("     %s", r.Suggestion.Description)
		}
	}
	logger.Info("")
}
