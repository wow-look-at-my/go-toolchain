package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-toolchain/src/build"
	"github.com/wow-look-at-my/go-toolchain/src/lint"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"github.com/wow-look-at-my/go-toolchain/src/summary"
	gotest "github.com/wow-look-at-my/go-toolchain/src/test"
	gotrace "github.com/wow-look-at-my/go-toolchain/src/trace"
	"github.com/wow-look-at-my/go-toolchain/src/vet"
)

// activeTrace collects fine-grained trace events for Chrome trace export.
var activeTrace *gotrace.Trace

var (
	outputDir     = "build"
	jsonOutput    bool
	verbose       bool
	cacheMisses   bool
	generateHash  string
	dupcode bool
	lintThreshold float64
	lintMinNodes  int
	cgoEnabled    bool
)

// skipCache returns true for subcommands that should not enable GOCACHEPROG.
func skipCache(name string) bool {
	switch name {
	case "cacheprog", "version", "install", "update", "release":
		return true
	}
	return false
}

var rootCmd = &cobra.Command{
	Use:          "go-toolchain",
	Short:        "Build Go projects with coverage enforcement",
	SilenceUsage: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if skipCache(cmd.Name()) {
			return nil
		}
		return enableCacheProg()
	},
	RunE: run,
}

func init() {
	rootCmd.Long = rootCmd.Short + "\n\nRuns go mod tidy, go test with coverage, and go build.\n\n" + installStatus()
	// Use PersistentFlags for flags shared with subcommands (like matrix)
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output coverage report as JSON")
	// rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Show test output line by line")
	rootCmd.PersistentFlags().StringVar(&generateHash, "generate", "", "Run go:generate directives matching this hash")
	// rootCmd.PersistentFlags().BoolVar(&dupcode, "dupcode", true, "Run near-duplicate code detection (warnings only)")
	rootCmd.PersistentFlags().Float64Var(&lintThreshold, "threshold", lint.DefaultThreshold, "Similarity threshold for duplicate detection (0.0-1.0)")
	rootCmd.PersistentFlags().IntVar(&lintMinNodes, "min-nodes", lint.DefaultMinNodes, "Minimum AST node count for duplicate detection")
	rootCmd.PersistentFlags().BoolVar(&cgoEnabled, "cgo", false, "Enable CGO (default: disabled for static binaries)")
	rootCmd.PersistentFlags().BoolVar(&cacheMisses, "cache-misses", false, "Show packages that missed the build cache")
	registerSelfProfileFlags()

	// Silent no-op flags — accepted without error for tool compatibility
	rootCmd.Flags().Bool("build", false, "")
	rootCmd.Flags().Bool("test", false, "")
	rootCmd.Flags().MarkHidden("build")
	rootCmd.Flags().MarkHidden("test")

	// Benchmark flags
	rootCmd.Flags().BoolVar(&noBenchmark, "no-benchmark", false, "Skip benchmarks after build")
	rootCmd.Flags().StringVar(&benchTime, "benchtime", "", "Duration or count for each benchmark (e.g. 5s, 1000x)")
	rootCmd.Flags().IntVarP(&benchCount, "count", "n", 1, "Number of times to run each benchmark")
	rootCmd.Flags().StringVar(&benchCPU, "cpu", "", "GOMAXPROCS values to test with (comma-separated, e.g. 1,2,4)")

	Register(rootCmd)
}

// Execute runs the root command.
func Execute() error {
	stop, err := startSelfProfile()
	if err != nil {
		return err
	}
	if stop != nil {
		defer stop()
	}
	defer printCacheStats(true)
	return rootCmd.Execute()
}

func run(cmd *cobra.Command, args []string) error {
	InitTimeline()

	if cacheMisses {
		tracker := newCacheMissTracker(os.Stderr)
		activeMissTracker = tracker
		vet.CompileStderr = tracker
		defer tracker.Print()
	}

	wd := startWatchdog(5 * time.Second)
	if wd != nil {
		activeWatchdog = wd
		defer func() {
			activeWatchdog = nil
			wd.stop()
		}()
	}

	modules := findGoModules()
	if len(modules) == 0 {
		return fmt.Errorf("no go.mod found — initialize with: go mod init <module-path>")
	}

	r := runner.New()
	startDir, _ := os.Getwd()

	// Create global trace for fine-grained events.
	activeTrace = gotrace.NewTrace()
	vet.ActiveTrace = activeTrace

	// Always write Chrome trace on exit, even if the build fails.
	defer func() {
		var entries []summary.TimelineEntry
		if tl := GetTimeline(); tl != nil {
			entries = tl.Entries()
		}
		tracePath := filepath.Join(os.TempDir(), "go-toolchain-profile", "trace.json")
		if err := gotrace.WriteChrome(tracePath, entries, activeTrace); err != nil {
			fmt.Fprintf(os.Stderr, "==> Warning: failed to write Chrome trace: %v\n", err)
		}
	}()

	// Accumulate summary data across all modules; write once at the end.
	var allSummary summary.SummaryData

	for i, modDir := range modules {
		if len(modules) > 1 {
			if i > 0 {
				fmt.Println()
			}
			fmt.Printf("==> Module: %s\n", modDir)
		}

		if modDir != "." {
			if err := os.Chdir(filepath.Join(startDir, modDir)); err != nil {
				return fmt.Errorf("failed to enter %s: %w", modDir, err)
			}
		}

		if err := runWithRunner(r, &allSummary); err != nil {
			return err
		}
	}

	// Populate timeline data for Gantt chart
	if tl := GetTimeline(); tl != nil {
		allSummary.Timeline = tl.Entries()
	}

	// Write GitHub Step Summary once after all modules complete
	if writeErr := summary.Write(&allSummary); writeErr != nil {
		fmt.Fprintf(os.Stderr, "==> Warning: failed to write step summary: %v\n", writeErr)
	}

	// Export OTel traces (no-op if OTEL_EXPORTER_OTLP_ENDPOINT is unset).
	if tl := GetTimeline(); tl != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := gotrace.Export(ctx, tl.Entries()); err != nil {
			fmt.Fprintf(os.Stderr, "==> Warning: failed to export traces: %v\n", err)
		}

	}

	return nil
}

// findGoModules searches for go.mod files in the current directory and subdirectories.
func findGoModules() []string {
	// Check current directory first
	if _, err := os.Stat("go.mod"); err == nil {
		return []string{"."}
	}

	// Search subdirectories
	var found []string
	filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name != "." && (strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "go.mod" {
			found = append(found, filepath.Dir(path))
		}
		return nil
	})

	return found
}

func runWithRunner(r runner.CommandRunner, sd *summary.SummaryData) error {
	setupCGOEnvironment()
	return runWithRunnerOnce(r, false, sd)
}

func runWithRunnerOnce(r runner.CommandRunner, isRetry bool, sd *summary.SummaryData) error {
	quiet := jsonOutput

	// Check for dep updates before tests so we don't run the full
	// test suite twice when a dependency is outdated.
	if !quiet && !isRetry {
		depChecker := CheckOutdatedDeps()
		if WaitForOutdatedDeps(depChecker) {
			fmt.Println()
		}
	}

	filesChanged, testResult, err := RunTestsWithCoverage(r, quiet)
	if err != nil {
		return err
	}

	// If vet applied fixes, re-run tests with the corrected code
	if !isRetry && filesChanged {
		fmt.Println("\n==> Files changed, rebuilding...")
		return runWithRunnerOnce(r, true, sd)
	}

	br, err := runBuildPhase(r, quiet)
	if err != nil {
		return err
	}

	// Accumulate data for the GitHub Step Summary
	if testResult != nil && sd != nil {
		sd.TestCases = append(sd.TestCases, testResult.TestCases...)
		// Use the last module's coverage as the overall coverage
		sd.Coverage = &testResult.Coverage
		if br != nil {
			sd.Benchmarks = br.Report
			sd.BenchComp = br.Comparison
		}
	}

	return nil
}

func runBuildPhase(r runner.CommandRunner, quiet bool) (*benchResult, error) {
	targets, err := build.ResolveBuildTargets(r)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
	}
	ensureBuildDirInGitignore()
	info, err := collectGitInfo()
	if err != nil {
		return nil, err
	}
	if err := checkDirtyInCI(info); err != nil {
		return nil, err
	}
	ldflags := info.ldflags()
	if !quiet {
		fmt.Printf("==> Embedding version: %s\n", info)
	}
	inDocker := build.InDocker()
	for _, t := range targets {
		outputName := t.OutputName
		if inDocker {
			outputName = build.BinaryName(outputName, runtime.GOOS, runtime.GOARCH)
		}
		outPath := filepath.Join(outputDir, outputName)
		var buildStep *step
		if !quiet {
			buildStep = logStep(fmt.Sprintf("go build -o %s %s", outPath, t.ImportPath))
		}
		var onFirstOutput func()
		if buildStep != nil {
			onFirstOutput = buildStep.noteOutput
		}
		job := buildJob{
			srcPath:    t.ImportPath,
			outputPath: outPath,
			ldflags:    ldflags,
		}
		if err := runBuild(r, job, onFirstOutput); err != nil {
			return nil, fmt.Errorf("go build failed: %w", err)
		}
		if buildStep != nil {
			buildStep.done()
		}
	}

	if !quiet {
		fmt.Println("==> Build successful")
	}

	if !noBenchmark {
		br, err := runBenchmarkInBuild(r)
		if err != nil {
			return nil, err
		}
		return br, nil
	}

	return nil, nil
}

// RunTestsWithCoverage runs go mod tidy, go vet, tests with coverage, and
// checks coverage against the threshold. Used by both the default command
// and the matrix command.
// Returns (filesChanged, testResult, error) where filesChanged indicates if vet applied any fixes.
func RunTestsWithCoverage(r runner.CommandRunner, quiet bool) (bool, *gotest.TestResult, error) {
	// Fix any v0.0.0 dependencies before go mod tidy
	if err := FixBogusDepsVersions(r); err != nil {
		return false, nil, err
	}

	// Handle vanity-URL modules: inject replace directives for unreachable hosts
	vanityReplaces, vanityErr := injectVanityReplaces()
	if vanityErr != nil {
		return false, nil, fmt.Errorf("vanity URL handling failed: %w", vanityErr)
	}

	var modTidyStep *step
	if !quiet {
		modTidyStep = logStep("go mod tidy")
	}
	timedStderr := newTimedLineWriter(os.Stderr)
	proc, err := runner.Cmd("go", "mod", "tidy", "-v").WithStderrWriter(timedStderr).WithOnFirstOutput(func() {
		if modTidyStep != nil {
			modTidyStep.noteOutput()
		}
	}).Run(r)
	if err != nil {
		return false, nil, fmt.Errorf("go mod tidy failed: %w", err)
	}
	if err := proc.Wait(); err != nil {
		if _, statErr := os.Stat("go.mod"); statErr != nil {
			return false, nil, fmt.Errorf("no go.mod found — initialize with: go mod init <module-path>")
		}
		return false, nil, fmt.Errorf("go mod tidy failed: %w", err)
	}
	timedStderr.Flush()
	if modTidyStep != nil {
		modTidyStep.done()
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

	// Defer removal of vanity replace directives until after all pipeline
	// stages complete. Tests and build need the replaces to resolve modules
	// when the vanity host is unreachable.
	defer func() {
		_ = removeVanityReplaces(vanityReplaces)
	}()

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
	fix := os.Getenv("CI") == "" // disable auto-fix on CI
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

	// Use a deterministic path so Go's test cache keys (which include
	// -coverprofile=<path>) are stable across runs.
	coverDir := filepath.Join(os.TempDir(), "go-toolchain-cov")
	os.MkdirAll(coverDir, 0o755)
	coverFile := filepath.Join(coverDir, "coverage.out")
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
			fmt.Println("\n==> Test failures:")
			fmt.Print(colorRed + result.FailureOutput + colorReset)
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
		fmt.Println("\n==> Package coverage:")
		report.Print()

		fmt.Printf("\n==> Total coverage: %s\n", colorPct(ColorPct{Pct: report.Total, Format: "%.1f%%"}))
	}

	// Coverage enforcement: default 80%, or watermark-2.5% if lower.
	var effectiveMin float32 = 80.0
	wm, wmExists, wmErr := gotest.GetWatermark(".")
	if wmErr != nil {
		// Watermark read failed (e.g., xattrs not supported) - warn and use default
		if !quiet {
			fmt.Printf("==> Warning: %v (using default %.0f%%)\n", wmErr, effectiveMin)
		}
		wmExists = false
	}
	if wmExists {
		grace := wm - 2.5
		if grace < effectiveMin {
			effectiveMin = grace
		}
		if !quiet {
			fmt.Printf("==> Watermark: %.1f%% (effective minimum: %.1f%%)\n", wm, effectiveMin)
		}
		// Ratchet up: update watermark if coverage improved
		if report.Total > wm {
			if err := gotest.SetWatermark(".", report.Total); err != nil {
				if !quiet {
					fmt.Printf("==> Warning: failed to update watermark: %v\n", err)
				}
			} else if !quiet {
				fmt.Printf("==> Watermark updated: %.1f%% -> %.1f%%\n", wm, report.Total)
			}
		}
	}

	// Round to 1 decimal place for comparison (same precision as display)
	roundedTotal := float32(math.Round(float64(report.Total)*10) / 10)
	roundedMin := float32(math.Round(float64(effectiveMin)*10) / 10)
	if roundedTotal < roundedMin {
		// Calculate total uncovered statements across all packages
		var totalUncovered int
		for _, pkg := range report.Packages {
			totalUncovered += pkg.Uncovered()
		}
		// Allow reduced coverage if fewer than 10 statements are uncovered
		// (e.g. small programs where main() can't be easily covered)
		if totalUncovered < 10 {
			if !quiet {
				fmt.Printf("==> Coverage %.1f%% is below minimum %.1f%%, but only %d statements uncovered — allowing\n", report.Total, effectiveMin, totalUncovered)
			}
		} else {
			return false, result, fmt.Errorf("coverage %.1f%% is below minimum %.1f%%", report.Total, effectiveMin)
		}
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
		fmt.Println("==> Checking for near-duplicate code")
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

	fmt.Printf("\n%s near-duplicate code: found %d pair(s)%s\n", colorYellow, len(reports), colorReset)
	for i, r := range reports {
		fmt.Printf("  %d. %.0f%% similar: %s (%s:%d) and %s (%s:%d)\n",
			i+1, r.Similarity*100,
			r.FuncA, r.FileA, r.LineA,
			r.FuncB, r.FileB, r.LineB,
		)
		if verbose {
			fmt.Printf("     %s\n", r.Suggestion.Description)
		}
	}
	fmt.Println()
}
