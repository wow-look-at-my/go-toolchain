package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-toolchain/src/build"
	"github.com/wow-look-at-my/go-toolchain/src/lint"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	gotest "github.com/wow-look-at-my/go-toolchain/src/test"
	"github.com/wow-look-at-my/go-toolchain/src/vet"
)

var (
	outputDir     = "build"
	jsonOutput    bool
	verbose       bool
	generateHash  string
	dupcode bool
	lintThreshold float64
	lintMinNodes  int
	cgoEnabled    bool
)

var rootCmd = &cobra.Command{
	Use:          "go-toolchain",
	Short:        "Build Go projects with coverage enforcement",
	SilenceUsage: true,
	RunE:         run,
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
	return rootCmd.Execute()
}

func run(cmd *cobra.Command, args []string) error {
	modules := findGoModules()
	if len(modules) == 0 {
		return fmt.Errorf("no go.mod found — initialize with: go mod init <module-path>")
	}

	r := runner.New()
	startDir, _ := os.Getwd()

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

		if err := runWithRunner(r); err != nil {
			return err
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

func runWithRunner(r runner.CommandRunner) error {
	return runWithRunnerOnce(r, false)
}

func runWithRunnerOnce(r runner.CommandRunner, isRetry bool) error {
	quiet := jsonOutput

	// Check for dep updates before tests so we don't run the full
	// test suite twice when a dependency is outdated.
	if !quiet && !isRetry {
		depChecker := CheckOutdatedDeps()
		if WaitForOutdatedDeps(depChecker) {
			fmt.Println()
		}
	}

	filesChanged, err := RunTestsWithCoverage(r, quiet)
	if err != nil {
		return err
	}

	// If vet applied fixes, re-run tests with the corrected code
	if !isRetry && filesChanged {
		fmt.Println("\n==> Files changed, rebuilding...")
		return runWithRunnerOnce(r, true)
	}

	if err := runBuildPhase(r, quiet); err != nil {
		return err
	}

	return nil
}

func runBuildPhase(r runner.CommandRunner, quiet bool) error {
	targets, err := build.ResolveBuildTargets(r)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
	}
	info := collectGitInfo()
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
			return fmt.Errorf("go build failed: %w", err)
		}
		if buildStep != nil {
			buildStep.done()
		}
	}

	if !quiet {
		fmt.Println("==> Build successful")
	}

	if !noBenchmark {
		if err := runBenchmarkInBuild(r); err != nil {
			return err
		}
	}

	return nil
}

// RunTestsWithCoverage runs go mod tidy, go vet, tests with coverage, and
// checks coverage against the threshold. Used by both the default command
// and the matrix command.
// Returns (filesChanged, error) where filesChanged indicates if vet applied any fixes.
func RunTestsWithCoverage(r runner.CommandRunner, quiet bool) (bool, error) {
	// Fix any v0.0.0 dependencies before go mod tidy
	if err := FixBogusDepsVersions(r); err != nil {
		return false, err
	}

	// Handle vanity-URL modules: inject replace directives for unreachable hosts
	vanityReplaces, vanityErr := injectVanityReplaces()
	if vanityErr != nil {
		return false, fmt.Errorf("vanity URL handling failed: %w", vanityErr)
	}

	var modTidyStep *step
	if !quiet {
		modTidyStep = logStep("go mod tidy")
	}
	timedStderr := newTimedLineWriter(os.Stderr)
	proc, err := runner.Cmd("go", "mod", "tidy").WithStderrWriter(timedStderr).WithOnFirstOutput(func() {
		if modTidyStep != nil {
			modTidyStep.noteOutput()
		}
	}).Run(r)
	if err != nil {
		return false, fmt.Errorf("go mod tidy failed: %w", err)
	}
	if err := proc.Wait(); err != nil {
		if _, statErr := os.Stat("go.mod"); statErr != nil {
			return false, fmt.Errorf("no go.mod found — initialize with: go mod init <module-path>")
		}
		return false, fmt.Errorf("go mod tidy failed: %w", err)
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
			return false, fmt.Errorf("go generate failed: %w", err)
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
		proc, err := runner.Cmd("go", "mod", "tidy").WithOnFirstOutput(func() {
			if tidyStep2 != nil {
				tidyStep2.noteOutput()
			}
		}).Run(r)
		if err != nil {
			return false, fmt.Errorf("go mod tidy failed: %w", err)
		}
		if err := proc.Wait(); err != nil {
			return false, fmt.Errorf("go mod tidy failed: %w", err)
		}
		if tidyStep2 != nil {
			tidyStep2.done()
		}
	}

	// Remove vanity replace directives now that tidy is done
	if err := removeVanityReplaces(vanityReplaces); err != nil {
		return false, fmt.Errorf("failed to clean up vanity replaces: %w", err)
	}

	var vetStep *step
	if !quiet {
		vetStep = logStep("go vet ./...")
	}
	var vetPhaseStart time.Time
	var vetPhaseName string
	vetProgress := func(phase string) {
		if quiet {
			return
		}
		now := time.Now()
		if vetPhaseName != "" {
			fmt.Fprintf(os.Stderr, "    %s %s\n", vetPhaseName, fmtDuration(now.Sub(vetPhaseStart)))
		} else if vetStep != nil {
			vetStep.noteOutput()
		}
		vetPhaseName = phase
		vetPhaseStart = now
	}
	fix := os.Getenv("CI") == "" // disable auto-fix on CI
	filesChanged, err := vet.RunWithProgress(fix, vetProgress)
	if err != nil {
		return false, fmt.Errorf("vet failed: %w", err)
	}
	// Print the last phase timing
	if !quiet && vetPhaseName != "" {
		fmt.Fprintf(os.Stderr, "    %s %s\n", vetPhaseName, fmtDuration(time.Since(vetPhaseStart)))
	}
	if vetStep != nil {
		vetStep.done()
	}

	if dupcode {
		runDuplicateCheck()
	}

	if err := checkFileLength("."); err != nil {
		return false, err
	}

	var testStep *step
	if !quiet {
		testStep = logStep("Running tests with coverage")
	}

	tmpDir, err := os.MkdirTemp("", "go-toolchain-*")
	if err != nil {
		return false, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	coverFile := filepath.Join(tmpDir, "coverage.out")

	var onTestOutput func()
	if testStep != nil {
		onTestOutput = testStep.noteOutput
	}
	result, testErr := gotest.RunTests(r, verbose, coverFile, onTestOutput)
	if result == nil {
		if testStep != nil {
			testStep.failed()
		}
		return false, fmt.Errorf("tests failed: %w", testErr)
	}
	if testErr != nil && testStep != nil {
		testStep.failed()
	} else if testStep != nil {
		testStep.done()
	}

	report := &result.Coverage

	// If tests failed, show failure details and return error (no coverage output)
	if testErr != nil {
		if !quiet && result.FailureOutput != "" {
			fmt.Println("\n==> Test failures:")
			fmt.Print(colorRed + result.FailureOutput + colorReset)
		}
		return false, fmt.Errorf("tests failed: %w", testErr)
	}

	if quiet {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "\t")
		if err := enc.Encode(report); err != nil {
			return false, fmt.Errorf("failed to encode JSON: %w", err)
		}
	} else {
		fmt.Println("\n==> Package coverage:")
		report.Print()

		fmt.Printf("\n==> Total coverage: %s\n", colorPct(ColorPct{Pct: report.Total, Format: "%.1f%%"}))
	}

	// Coverage enforcement: default 80%, or watermark-2.5% if lower
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
		return false, fmt.Errorf("coverage %.1f%% is below minimum %.1f%%", report.Total, effectiveMin)
	}

	return filesChanged, nil
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
