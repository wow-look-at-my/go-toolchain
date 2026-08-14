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

	"github.com/wow-look-at-my/go-toolchain/src/lint"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	gotest "github.com/wow-look-at-my/go-toolchain/src/test"
	gotrace "github.com/wow-look-at-my/go-toolchain/src/trace"
	"github.com/wow-look-at-my/go-toolchain/src/vet"
)

// RunTestsWithCoverage runs go mod tidy, go vet, tests with coverage, and
// checks coverage against the threshold. Used by both the default command
// and the matrix command.
// Returns (filesChanged, testResult, error) where filesChanged indicates if vet applied any fixes.
func RunTestsWithCoverage(r runner.CommandRunner, quiet bool) (bool, *gotest.TestResult, error) {
	// Fix any v0.0.0 dependencies before go mod tidy
	if err := FixBogusDepsVersions(r); err != nil {
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
	// Remove the injected vanity replace directives (and restore go.sum) when
	// this function returns, however it returns — including the early returns
	// when `go mod tidy` fails below. Registering the cleanup here, rather than
	// after tidy, is what guarantees a failed tidy cannot leave the injected
	// GitHub/GitLab mirror replaces festering in the user's go.mod. The replaces
	// stay active for tidy, generate, vet, tests, and build (all run before this
	// function returns).
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
	// On CI (CI=true) run the fixers in check-only mode: vet never writes, and
	// any change it would make — gofmt, a wow-look-at-my/testify fork or
	// gotest.tools import migration, or a testify cross-type cast — becomes a
	// hard error, so a non-canonical tree fails CI instead of passing green.
	// Locally (CI unset) the fixers rewrite the tree as before.
	fix := os.Getenv("CI") == ""
	filesChanged, err := vet.RunWithProgress(fix, vetProgress)
	if err != nil {
		// If in-process vet fails due to Go version mismatch (e.g. binary built
		// with Go 1.24 but project requires Go 1.25), fall back to external go vet
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
		} else if isCorruptExportData(err) {
			// A corrupt build-cache entry, not a source error. Recover once,
			// deterministically: drop the shared cache tier and rebuild the
			// damaged packages from source. Exactly one retry, and only when
			// that tier was actually in play, so this cannot loop.
			if !disableSharedBuildCache() {
				return false, nil, corruptExportDataError(err, false)
			}
			logger.Warn("⇒ Warning: vet failed on CORRUPT BUILD CACHE data (%s), not on your source: %s. Disabling the shared build cache (GOCACHEPROG) for the rest of this run and rebuilding those packages from source. Repeated occurrences mean the shared cache tier is serving damaged entries and needs inspecting.",
				invalidPackageNameMarker, strings.Join(corruptExportPackages(err), ", "))
			if vetPhaseStep != nil {
				vetPhaseStep.done()
				vetPhaseStep = nil
			}
			vetPhaseStep = logSubStep("vet: retry without the shared build cache", "main")
			filesChanged, err = vet.RunWithProgress(fix, vetProgress)
			if err != nil {
				if isCorruptExportData(err) {
					return false, nil, corruptExportDataError(err, true)
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

	// Vet is the last stage that can modify files (auto-fix). Fail fast here
	// rather than after the full test run. The vanity replaces injected above
	// are still active at this point (their removal is deferred to this
	// function's return), so the check runs against the tree as that cleanup
	// will restore it — the toolchain's own transient go.mod/go.sum mutation
	// must never count as dirt, while every real uncommitted change still
	// fails (see checkDirtyInCIWithVanityRestored).
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

	// Use a process-unique path to avoid collisions when tests call
	// RunTestsWithCoverage with mock runners — they write and then delete
	// this file, which would corrupt the outer go test's coverprofile.
	// With -count=1 already disabling test-result caching, cache-key
	// stability from a deterministic path is no longer required.
	coverDir := filepath.Join(os.TempDir(), "go-toolchain-cov")
	os.MkdirAll(coverDir, 0o755)
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
		// Build set of tests that have subtests so we only trace leaf tests
		// (parent durations include children and would overlap).
		hasSubtest := make(map[string]bool)
		for _, tc := range result.TestCases {
			if i := strings.LastIndex(tc.Test, "/"); i > 0 {
				hasSubtest[tc.Package+"."+tc.Test[:i]] = true
			}
		}
		for _, tc := range result.TestCases {
			if tc.Elapsed <= 0 || tc.End.IsZero() {
				continue
			}
			if hasSubtest[tc.Package+"."+tc.Test] {
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

	// Coverage enforcement: default 80%, or watermark-2.5% if lower.
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
