package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-containers/set"
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
	cacheMisses    bool
	generateHash   string
	dupcode        bool
	lintThreshold  float64
	lintMinNodes   int
	cgoEnabled     bool
	countGenerated bool
)

// skipUpToDateCheck reports whether cmd or an ancestor skips the
// fingerprint "up to date" fast exit. A subcommand inherits it.
func skipUpToDateCheck(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "version", "install", "release":
			return true
		}
	}
	return false
}

// unguardedCmds print no build result, so a capture hides nothing. Depth: docs/AGENT-OUTPUT-GUARD.md.
var unguardedCmds = set.Of("version")

// toolchainlessCmds run no go command; resolving the fork would download a compiler to answer a question about this binary.
var toolchainlessCmds = set.Of("version", "verify-identical")

// checkTargetFlags validates --targets and --cosmo-platforms, for the commands
// that have them. The build path parses them again where it uses them; this
// only moves the rejection ahead of the toolchain download.
func checkTargetFlags(cmd *cobra.Command) error {
	if cmd.Flags().Lookup("targets") == nil {
		return nil
	}
	if _, err := resolveMatrixPlatforms(); err != nil {
		return err
	}
	_, err := parseCosmoPlatforms(cosmoPlatforms)
	return err
}

// skipToolchain reports whether cmd runs without the gosmopolitan toolchain.
func skipToolchain(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if toolchainlessCmds.Contains(c.Name()) {
			return true
		}
	}
	return false
}

// skipAgentGuard reports whether cmd or an ancestor prints no build result.
func skipAgentGuard(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if unguardedCmds.Contains(c.Name()) {
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
		// Install the logger ahead of the output guard, so every
		// command's output honors the requested level.
		if err := initLogging(cmd); err != nil {
			return err
		}
		// Snapshot env before phases add vars, so fingerprint matches what the next run checks.
		captureRunEnv()
		// Abort if the agent hides our output, unless this is cacheprog (see skipAgentGuard).
		if !skipAgentGuard(cmd) {
			guardAgainstAgentOutputCapture()
		}
		// A target set nothing can build is rejected before a compiler is fetched for it.
		if err := checkTargetFlags(cmd); err != nil {
			return err
		}
		// After cobra parses, so --help and a mistyped flag cost no compiler.
		if !skipToolchain(cmd) {
			if err := EnsureGoVersion(); err != nil {
				// Drop the previous run's binaries so a failed run cannot pass for a good run (see staleoutputs.go).
				discardBuildOutputsFromCWD()
				return fmt.Errorf("go bootstrap: %w", err)
			}
		}
		if skipUpToDateCheck(cmd) {
			return nil
		}
		if cmd.Parent() == nil && isUpToDate(runner.New()) {
			logger.Output("⇒ Up to date, nothing to do")
			ReportUpdateCheck()
			os.Exit(0)
		}
		return nil
	},
	RunE: run,
}

func init() {
	rootCmd.Long = rootCmd.Short + "\n\nRuns go mod tidy, go test with coverage, and go build.\n\n" + installStatus()
	// Use PersistentFlags for flags shared with subcommands (like matrix)
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output coverage report as JSON")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output: debug log level, plus per-test output lines")
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

	// Fingerprint covers the invoked flags; see flagFingerprint for why it excludes rootCmd itself.
	fingerprintFlags = rootCmd.Flags()
	// Kept apart: Flags() merges these in only at parse time.
	fingerprintPersistentFlags = rootCmd.PersistentFlags()

	Register(rootCmd)
}

// Execute runs the root command.
func Execute() error {
	defer emitBuildProfile()
	return rootCmd.Execute()
}

func run(cmd *cobra.Command, args []string) (err error) {
	InitTimeline()

	// Runs last (registered at the head) so a later phase's failure still discards
	// the binary the build just re-created.
	defer func() {
		if err != nil {
			discardBuildOutputs()
		}
	}()

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
		// A repo can own dats suites with no go.mod (the tested CLI need not
		// be Go); the suites ARE the run then.
		if hasDatsSuites(".") {
			return runDatsOnly()
		}
		return fmt.Errorf("no go.mod and no dats/ suites found — initialize a module with: go mod init <module-path>")
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

	// Collect per-action profiles; captureProfileTrace runs after WriteChrome (LIFO), before the final report.
	initBuildProfile()
	defer captureProfileTrace()

	// Accumulate summary data across all modules; write it at the end.
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

	// Write the GitHub Step Summary after all modules complete
	if writeErr := summary.Write(&allSummary); writeErr != nil {
		logger.Warn("⇒ Warning: failed to write step summary: %v", writeErr)
	}

	os.Chdir(startDir)

	// Fail before saveFingerprint when warnings exceed budget, so a failed
	// run is never stamped up-to-date.
	if err := checkWarningsGate(); err != nil {
		return err
	}

	saveFingerprint(r)
	return nil
}

// findGoModules searches for go.mod files in the current directory and subdirectories.
func findGoModules() []string {
	// Check the current directory before walking subdirectories
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
	// Delete outputs before any phase runs, so a run that dies anywhere
	// leaves nothing behind to mistake for a result.
	if err := clearBuildOutputs(r); err != nil {
		return err
	}
	return runWithRunnerOnce(r, false, sd)
}

func runWithRunnerOnce(r runner.CommandRunner, isRetry bool, sd *summary.SummaryData) error {
	quiet := jsonOutput

	// Check for dep updates before tests so we don't run the full
	// test suite again when a dependency is outdated.
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

	if err := integration.Run(context.Background(), "tests"); err != nil {
		return err
	}

	// Runs after the build phase (memlimit guard already cleaned up); a
	// failing suite fails before saveFingerprint.
	if err := runDatsPhase(quiet, builtArtifacts); err != nil {
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
	// Runs before go-toolchain writes its own artifacts, catching only uncommitted source.
	if err := checkDirtyInCI(); err != nil {
		return nil, nil, err
	}

	if err := injectMemLimitGuard(quiet); err != nil {
		return nil, nil, err
	}
	// Build-time-only artifact; remove it as soon as it is compiled in, so it never lingers in the tree.
	defer cleanupMemLimitGuards()

	targets, err := build.ResolveBuildTargets(r)
	if err != nil {
		return nil, nil, err
	}

	// The same fat APE the matrix path publishes, so nothing here can differ from what ships.
	warnCGOUnavailable(true, false)
	forkEnv, err := resolveForkBuildEnv(true)
	if err != nil {
		return nil, nil, err
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, nil, fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
	}
	ensureBuildDirInGitignore()
	var artifacts []datsArtifact
	for _, t := range targets {
		// The APE carries no platform suffix: the same file runs on every host.
		outPath := filepath.Join(outputDir, build.BinaryName(t.OutputName, cosmoOS, cosmoFatArch))
		var buildStep *step
		if !quiet {
			buildStep = logStep(fmt.Sprintf("go build -o %s %s", outPath, t.ImportPath))
		}
		var onFirstOutput func()
		if buildStep != nil {
			onFirstOutput = buildStep.noteOutput
		}
		if err := runBuild(r, forkEnv.apeJob(t.ImportPath, outPath), onFirstOutput); err != nil {
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
