package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-toolchain/src/build"
	"github.com/wow-look-at-my/go-toolchain/src/codeql"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/integration"
	"github.com/wow-look-at-my/go-toolchain/src/lint"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"github.com/wow-look-at-my/go-toolchain/src/summary"
	gotrace "github.com/wow-look-at-my/go-toolchain/src/trace"
	"github.com/wow-look-at-my/go-toolchain/src/vet"
)

// activeTrace collects fine-grained trace events for Chrome trace export.
var activeTrace *gotrace.Trace

var (
	outputDir      = "build"
	jsonOutput     bool
	verbose        bool
	logLevel       string
	cacheMisses    bool
	generateHash   string
	dupcode        bool
	lintThreshold  float64
	lintMinNodes   int
	cgoEnabled     bool
	countGenerated bool
)

// skipCache reports whether cmd or any of its ancestors is a command tree
// that should not enable GOCACHEPROG. Cobra passes the leaf command to
// PersistentPreRunE, so e.g. `version raw` arrives with cmd.Name() == "raw"
// and must still inherit the skip from its parent `version`.
func skipCache(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "cacheprog", "version", "install", "release":
			return true
		}
	}
	return false
}

var rootCmd = &cobra.Command{
	Use:          "go-toolchain",
	Short:        "Build Go projects with coverage enforcement",
	SilenceUsage: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Install the leveled global logger first — for EVERY command,
		// including the skipCache-exempt ones, and before the Claude output
		// guard — so all subsequent output honors the requested level.
		if err := initLogging(cmd); err != nil {
			return err
		}
		if skipCache(cmd) {
			return nil
		}
		// Abort before doing any work if Claude is hiding our output — piping
		// it, redirecting it to a file, or discarding it — instead of letting
		// the coverage report and build/test failures print where it can read
		// them.
		guardAgainstClaudeOutputCapture()
		if cmd.Parent() == nil && isUpToDate(runner.New()) {
			logger.Output("⇒ Up to date, nothing to do")
			ReportUpdateCheck()
			os.Exit(0)
		}
		return enableCacheProg()
	},
	RunE: run,
}

func init() {
	rootCmd.Long = rootCmd.Short + "\n\nRuns go mod tidy, go test with coverage, and go build.\n\n" + installStatus()
	// Use PersistentFlags for flags shared with subcommands (like matrix)
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output coverage report as JSON")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output: debug log level, plus per-test output lines")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Minimum log level: debug, info, warn, error, or silent")
	rootCmd.PersistentFlags().StringVar(&generateHash, "generate", "", "Run go:generate directives matching this hash")
	// rootCmd.PersistentFlags().BoolVar(&dupcode, "dupcode", true, "Run near-duplicate code detection (warnings only)")
	rootCmd.PersistentFlags().Float64Var(&lintThreshold, "threshold", lint.DefaultThreshold, "Similarity threshold for duplicate detection (0.0-1.0)")
	rootCmd.PersistentFlags().IntVar(&lintMinNodes, "min-nodes", lint.DefaultMinNodes, "Minimum AST node count for duplicate detection")
	rootCmd.PersistentFlags().BoolVar(&cgoEnabled, "cgo", false, "Enable CGO (default: disabled for static binaries)")
	rootCmd.PersistentFlags().BoolVar(&cacheMisses, "cache-misses", false, "Show packages that missed the build cache")
	rootCmd.PersistentFlags().BoolVar(&countGenerated, "count-generated", false, "Count generated files (Code generated ... DO NOT EDIT.) in the file length check instead of skipping them")
	rootCmd.PersistentFlags().BoolVar(&noProfile, "no-profile", false, "Skip the per-action build profile (actiongraph collection, console section, and profile.json)")

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
	defer printCacheStats(true)
	return rootCmd.Execute()
}

func run(cmd *cobra.Command, args []string) error {
	InitTimeline()

	if cacheMisses {
		tracker := newCacheMissTracker(os.Stderr)
		activeMissTracker = tracker
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
		tracePath := filepath.Join(profileDir(), "trace.json")
		if err := gotrace.WriteChrome(tracePath, entries, activeTrace); err != nil {
			logger.Warn("⇒ Warning: failed to write Chrome trace: %v", err)
		}
	}()

	// Collect per-action build profiles (unless --no-profile). The deferred
	// capture parses the actiongraph dumps and records per-action lanes into
	// the trace; registered AFTER the trace-write defer so it runs first
	// (LIFO), even when the build fails. The final report (console + JSON)
	// is emitted later by printCacheStats, once the cache daemon has drained.
	initBuildProfile()
	defer captureProfileTrace()

	// Accumulate summary data across all modules; write once at the end.
	var allSummary summary.SummaryData

	for i, modDir := range modules {
		if len(modules) > 1 {
			if i > 0 {
				logger.Info("")
			}
			logger.Info("⇒ Module: %s", modDir)
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

	if err := maybeSubmitDeps(); err != nil {
		return err
	}

	// Populate timeline data for Gantt chart
	if tl := GetTimeline(); tl != nil {
		allSummary.Timeline = tl.Entries()
	}

	// Write GitHub Step Summary once after all modules complete
	if writeErr := summary.Write(&allSummary); writeErr != nil {
		logger.Warn("⇒ Warning: failed to write step summary: %v", writeErr)
	}

	// Export OTel traces (no-op if OTEL_EXPORTER_OTLP_ENDPOINT is unset).
	if tl := GetTimeline(); tl != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := gotrace.Export(ctx, tl.Entries()); err != nil {
			logger.Warn("⇒ Warning: failed to export traces: %v", err)
		}

	}

	os.Chdir(startDir)

	// Warnings budget: fail the run — after every phase has completed and
	// every warning has been printed — when it emitted more than maxWarnings
	// warnings. Before saveFingerprint, so a gate-failed run is not stamped
	// up-to-date (the next run must not fast-exit past the failure).
	if err := checkWarningsGate(); err != nil {
		return err
	}

	saveFingerprint(r)
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
			logger.Info("")
		}
	}

	filesChanged, testResult, err := RunTestsWithCoverage(r, quiet)
	if err != nil {
		return err
	}

	// If vet applied fixes, re-run tests with the corrected code
	if !isRetry && filesChanged {
		logger.Info("\n⇒ Files changed, rebuilding...")
		return runWithRunnerOnce(r, true, sd)
	}

	if codeql.Enabled() {
		ex := logStep("codeql extract")
		if err := codeql.Extract(r); err != nil {
			ex.failed()
			return err
		}
		ex.done()
	}

	br, builtArtifacts, err := runBuildPhase(r, quiet)
	if err != nil {
		return err
	}

	if err := integration.Run("tests"); err != nil {
		return err
	}

	// Run the module's dats suites (if any) against the binaries just built.
	// After the build phase so the transient memlimit guard is already
	// cleaned up; an error here fails the run before saveFingerprint, so a
	// failing suite is never stamped up-to-date.
	if err := runDatsPhase(r, quiet, builtArtifacts); err != nil {
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

// runBuildPhase builds every target and runs benchmarks. It also returns the
// built artifacts so the dats phase can stage host-runnable copies for suites.
func runBuildPhase(r runner.CommandRunner, quiet bool) (*benchResult, []datsArtifact, error) {
	// Validate the working tree before go-toolchain writes any of its own
	// build-time artifacts (the transient GOMEMLIMIT guard, the build/ output
	// dir, the .gitignore upkeep below). checkDirtyInCI is meant to catch
	// uncommitted source and the tidy/vet auto-fixes that ran earlier — not
	// go-toolchain's own generated files, which would otherwise make a
	// consumer's first CI build fail on artifacts it never authored.
	if err := checkDirtyInCI(); err != nil {
		return nil, nil, err
	}

	if err := injectMemLimitGuard(quiet); err != nil {
		return nil, nil, err
	}
	// The guard is a build-time-only artifact; remove it once the build below
	// has compiled it in, so it never lingers in the working tree.
	defer cleanupMemLimitGuards()

	targets, err := build.ResolveBuildTargets(r)
	if err != nil {
		return nil, nil, err
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, nil, fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
	}
	ensureBuildDirInGitignore()
	inDocker := build.InDocker()
	var artifacts []datsArtifact
	for _, t := range targets {
		outputName := t.OutputName
		if inDocker {
			// hostos: in-docker names carry the HOST platform, and a cosmo
			// fat APE reports runtime.GOOS=="cosmo" on every host.
			outputName = build.BinaryName(outputName, hostos.GOOS(), runtime.GOARCH)
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
		}
		if err := runBuild(r, job, onFirstOutput); err != nil {
			return nil, nil, fmt.Errorf("go build failed: %w", err)
		}
		if buildStep != nil {
			buildStep.done()
		}
		artifacts = append(artifacts, datsArtifact{
			sourcePath: outPath,
			name:       datsArtifactName(t.OutputName, hostos.GOOS()),
		})
	}

	if !quiet {
		logger.Info("⇒ Build successful")
	}

	if !noBenchmark {
		br, err := runBenchmarkInBuild(r)
		if err != nil {
			return nil, nil, err
		}
		return br, artifacts, nil
	}

	return nil, artifacts, nil
}
