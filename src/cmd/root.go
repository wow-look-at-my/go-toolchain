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
	"strings"

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
	addWatermark  bool
	doRemoveWmark bool
	generateHash  string
	fix           = os.Getenv("CI") == "" // disable auto-fix on CI
	dupcode       bool
	lintThreshold float64
	lintMinNodes  int
)

var rootCmd = &cobra.Command{
	Use:          "go-toolchain",
	Short:        "Build Go projects with coverage enforcement",
	SilenceUsage: true,
	RunE:         run,
}

func init() {
	rootCmd.Long = rootCmd.Short + "\n\nRuns go mod tidy, go test with coverage, and go build. Use --add-watermark to enforce coverage floors.\n\n" + installStatus()
	// Use PersistentFlags for flags shared with subcommands (like matrix)
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output coverage report as JSON")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Show test output line by line")
	rootCmd.PersistentFlags().BoolVar(&addWatermark, "add-watermark", false, "Store current coverage as watermark (enforced on future runs)")
	rootCmd.PersistentFlags().BoolVar(&doRemoveWmark, "remove-watermark", false, "Remove the coverage watermark")
	rootCmd.PersistentFlags().MarkHidden("remove-watermark")
	rootCmd.PersistentFlags().StringVar(&generateHash, "generate", "", "Run go:generate directives matching this hash")
	rootCmd.PersistentFlags().BoolVar(&fix, "fix", fix, "Auto-fix linter violations")
	rootCmd.PersistentFlags().BoolVar(&dupcode, "dupcode", true, "Run near-duplicate code detection (warnings only)")
	rootCmd.PersistentFlags().Float64Var(&lintThreshold, "threshold", lint.DefaultThreshold, "Similarity threshold for duplicate detection (0.0-1.0)")
	rootCmd.PersistentFlags().IntVar(&lintMinNodes, "min-nodes", lint.DefaultMinNodes, "Minimum AST node count for duplicate detection")

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
	r := runner.New()
	return runWithRunner(r)
}

func runWithRunner(r runner.CommandRunner) error {
	quiet := jsonOutput
	// Handle --remove-watermark early, before any build steps
	if doRemoveWmark {
		return handleRemoveWatermark()
	}

	// Start async dependency freshness check (reports at end)
	var depChecker *DepChecker
	if !quiet {
		depChecker = CheckOutdatedDeps()
		defer WaitForOutdatedDeps(depChecker)
	}

	if err := RunTestsWithCoverage(r, quiet); err != nil {
		return err
	}

	targets, err := build.ResolveBuildTargets(r)
	if err != nil {
		return err
	}

	if len(targets) == 0 {
		// Library-only project, just verify everything compiles
		if !quiet {
			fmt.Println("==> go build ./... (no main packages found)")
		}
		proc, err := runner.Cmd("go", "build", "./...").Run(r)
		if err != nil {
			return fmt.Errorf("go build failed: %w", err)
		}
		if err := proc.Wait(); err != nil {
			return fmt.Errorf("go build failed: %w", err)
		}
	} else {
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
		}
		info := collectGitInfo()
		ldflags := info.ldflags()
		if !quiet {
			fmt.Printf("==> Embedding version: %s\n", info)
		}
		for _, t := range targets {
			outPath := filepath.Join(outputDir, t.OutputName)
			if !quiet {
				fmt.Printf("==> go build -o %s %s\n", outPath, t.ImportPath)
			}
			proc, err := runner.Cmd("go", "build", "-ldflags", ldflags, "-o", outPath, t.ImportPath).Run(r)
			if err != nil {
				return fmt.Errorf("go build failed: %w", err)
			}
			if err := proc.Wait(); err != nil {
				return fmt.Errorf("go build failed: %w", err)
			}
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
func RunTestsWithCoverage(r runner.CommandRunner, quiet bool) error {
	// Fix any v0.0.0 dependencies before go mod tidy
	if err := FixBogusDepsVersions(r); err != nil {
		return err
	}

	if !quiet {
		fmt.Println("==> go mod tidy")
	}
	proc, err := runner.Cmd("go", "mod", "tidy").Run(r)
	if err != nil {
		return fmt.Errorf("go mod tidy failed: %w", err)
	}
	if err := proc.Wait(); err != nil {
		if _, statErr := os.Stat("go.mod"); statErr != nil {
			return fmt.Errorf("no go.mod found — initialize with: go mod init <module-path>")
		}
		return fmt.Errorf("go mod tidy failed: %w", err)
	}

	if needsGenerate() {
		if !quiet {
			fmt.Println("==> go generate ./...")
		}
		if err := runGenerate(quiet, generateHash); err != nil {
			return fmt.Errorf("go generate failed: %w", err)
		}
		// Run tidy again after generate in case new imports were added
		if !quiet {
			fmt.Println("==> go mod tidy (post-generate)")
		}
		proc, err := runner.Cmd("go", "mod", "tidy").Run(r)
		if err != nil {
			return fmt.Errorf("go mod tidy failed: %w", err)
		}
		if err := proc.Wait(); err != nil {
			return fmt.Errorf("go mod tidy failed: %w", err)
		}
	}

	if !quiet {
		fmt.Println("==> go vet ./...")
	}
	if err := vet.Run(fix); err != nil {
		return fmt.Errorf("vet failed: %w", err)
	}

	if dupcode {
		runDuplicateCheck()
	}

	if !quiet {
		fmt.Println("==> Running tests with coverage")
	}

	tmpDir, err := os.MkdirTemp("", "go-toolchain-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	coverFile := filepath.Join(tmpDir, "coverage.out")

	result, testErr := gotest.RunTests(r, verbose, coverFile)
	if result == nil {
		return fmt.Errorf("tests failed: %w", testErr)
	}

	report := &result.Coverage

	// If tests failed, show failure details and return error (no coverage output)
	if testErr != nil {
		if !quiet && result.FailureOutput != "" {
			fmt.Println("\n==> Test failures:")
			fmt.Print(colorRed + result.FailureOutput + colorReset)
		}
		return fmt.Errorf("tests failed: %w", testErr)
	}

	if quiet {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "\t")
		if err := enc.Encode(report); err != nil {
			return fmt.Errorf("failed to encode JSON: %w", err)
		}
	} else {
		fmt.Println("\n==> Package coverage:")
		report.Print()

		fmt.Printf("\n==> Total coverage: %s\n", colorPct(ColorPct{Pct: report.Total, Format: "%.1f%%"}))
	}

	// Handle --add-watermark: store watermark after coverage is computed
	if addWatermark {
		if err := gotest.SetWatermark(".", report.Total); err != nil {
			return fmt.Errorf("failed to set watermark: %w", err)
		}
		if !quiet {
			fmt.Printf("\n==> Watermark set to %.1f%% (will be enforced on future runs)\n", report.Total)
		}
	}

	// Coverage enforcement: default 80%, or watermark-2.5% if lower
	var effectiveMin float32 = 80.0
	wm, wmExists, wmErr := gotest.GetWatermark(".")
	if wmErr != nil {
		return fmt.Errorf("failed to read watermark: %w", wmErr)
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
				return fmt.Errorf("failed to update watermark: %w", err)
			}
			if !quiet {
				fmt.Printf("==> Watermark updated: %.1f%% -> %.1f%%\n", wm, report.Total)
			}
		}
	}

	// Round to 1 decimal place for comparison (same precision as display)
	roundedTotal := float32(math.Round(float64(report.Total)*10) / 10)
	roundedMin := float32(math.Round(float64(effectiveMin)*10) / 10)
	if roundedTotal < roundedMin {
		return fmt.Errorf("coverage %.1f%% is below minimum %.1f%%", report.Total, effectiveMin)
	}

	return nil
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

func handleRemoveWatermark() error {
	_, exists, err := gotest.GetWatermark(".")
	if err != nil {
		return fmt.Errorf("failed to read watermark: %w", err)
	}
	if !exists {
		fmt.Println("No watermark is set.")
		return nil
	}

	fmt.Println("Removing the coverage watermark disables the ratchet mechanism.")
	fmt.Println("This means coverage can drop without failing the build.")
	fmt.Print("Are you sure? [y/N] ")

	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))

	if line != "y" && line != "yes" {
		fmt.Println("Aborted.")
		return nil
	}

	if err := gotest.RemoveWatermark("."); err != nil {
		return fmt.Errorf("failed to remove watermark: %w", err)
	}
	fmt.Println("Watermark removed.")
	return nil
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
