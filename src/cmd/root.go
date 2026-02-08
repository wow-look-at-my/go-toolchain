package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-toolchain/src/build"
	gotest "github.com/wow-look-at-my/go-toolchain/src/test"
)

var (
	outputDir     = "build"
	covDetail     string
	minCoverage   float32
	jsonOutput    bool
	verbose       bool
	addWatermark  bool
	doRemoveWmark bool
)

var rootCmd = &cobra.Command{
	Use:          "go-toolchain",
	Short:        "Build Go projects with coverage enforcement",
	SilenceUsage: true,
	RunE:         run,
}

func init() {
	rootCmd.Long = rootCmd.Short + "\n\nRuns go mod tidy, go test with coverage, and go build. Fails if coverage is below threshold.\n\n" + installStatus()
	// Use PersistentFlags for flags shared with subcommands (like matrix)
	rootCmd.PersistentFlags().StringVar(&covDetail, "cov-detail", "", "Show detailed coverage: 'func' or 'file'")
	rootCmd.PersistentFlags().Float32Var(&minCoverage, "min-coverage", 80.0, "Minimum coverage percentage (0 = test only, no build)")
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output coverage report as JSON")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Show test output line by line")
	rootCmd.PersistentFlags().BoolVar(&addWatermark, "add-watermark", false, "Store current coverage as watermark (enforced on future runs)")
	rootCmd.PersistentFlags().BoolVar(&doRemoveWmark, "remove-watermark", false, "Remove the coverage watermark")
	rootCmd.PersistentFlags().MarkHidden("remove-watermark")

	// Benchmark flags
	rootCmd.Flags().BoolVar(&doBenchmark, "benchmark", false, "Run benchmarks after build")
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
	runner := &RealCommandRunner{Quiet: jsonOutput}
	return runWithRunner(runner)
}

func runWithRunner(runner CommandRunner) error {
	// Handle --remove-watermark early, before any build steps
	if doRemoveWmark {
		return handleRemoveWatermark()
	}

	testOnly := minCoverage == 0

	if testOnly && !jsonOutput {
		fmt.Println(warn("--min-coverage 0 runs tests ONLY. No binary will be produced."))
		fmt.Println(warn("This mode cannot bypass testing. To build, set --min-coverage > 0."))
		fmt.Println()
	}

	if err := RunTestsWithCoverage(runner); err != nil {
		return err
	}

	// Test-only mode: report and exit
	if testOnly {
		if !jsonOutput {
			fmt.Println("\n==> Test-only mode (--min-coverage 0), skipping build")
		}
		return fmt.Errorf("test-only mode")
	}

	targets, err := build.ResolveBuildTargets(runner)
	if err != nil {
		return err
	}

	if len(targets) == 0 {
		// Library-only project, just verify everything compiles
		if !jsonOutput {
			fmt.Println("==> go build ./... (no main packages found)")
		}
		if err := runner.Run("go", "build", "./..."); err != nil {
			return fmt.Errorf("go build failed: %w", err)
		}
	} else {
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
		}
		info := collectGitInfo()
		ldflags := info.ldflags()
		if !jsonOutput {
			fmt.Printf("==> Embedding version: %s\n", info)
		}
		for _, t := range targets {
			outPath := filepath.Join(outputDir, t.OutputName)
			if !jsonOutput {
				fmt.Printf("==> go build -o %s %s\n", outPath, t.ImportPath)
			}
			args := []string{"build", "-ldflags", ldflags, "-o", outPath, t.ImportPath}
			if err := runner.Run("go", args...); err != nil {
				return fmt.Errorf("go build failed: %w", err)
			}
		}
	}

	if !jsonOutput {
		fmt.Println("==> Build successful")
	}

	if doBenchmark {
		if err := runBenchmarkWithRunner(runner); err != nil {
			return err
		}
	}

	return nil
}

// RunTestsWithCoverage runs go mod tidy, go vet, tests with coverage, and
// checks coverage against the threshold. Used by both the default command
// and the matrix command.
func RunTestsWithCoverage(runner CommandRunner) error {
	if !jsonOutput {
		fmt.Println("==> go mod tidy")
	}
	if err := runner.Run("go", "mod", "tidy"); err != nil {
		if _, statErr := os.Stat("go.mod"); statErr != nil {
			return fmt.Errorf("no go.mod found — initialize with: go mod init <module-path>")
		}
		return fmt.Errorf("go mod tidy failed: %w", err)
	}

	if needsGenerate() {
		if !jsonOutput {
			fmt.Println("==> go generate ./...")
		}
		if err := runner.Run("go", "generate", "./..."); err != nil {
			return fmt.Errorf("go generate failed: %w", err)
		}
	}

	if !jsonOutput {
		fmt.Println("==> go vet ./...")
	}
	if err := runner.Run("go", "vet", "./..."); err != nil {
		return fmt.Errorf("go vet failed: %w", err)
	}

	if !jsonOutput {
		fmt.Println("==> Running tests with coverage")
	}

	tmpDir, err := os.MkdirTemp("", "go-toolchain-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	coverFile := filepath.Join(tmpDir, "coverage.out")

	result, testErr := gotest.RunTests(runner, verbose, coverFile)
	if result == nil {
		return fmt.Errorf("tests failed: %w", testErr)
	}

	report := &result.Coverage

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "\t")
		if err := enc.Encode(report); err != nil {
			return fmt.Errorf("failed to encode JSON: %w", err)
		}
	} else {
		fmt.Println("\n==> Package coverage:")
		for _, p := range report.Packages {
			// In non-verbose mode, hide passing packages when there are failures
			if testErr != nil && !verbose && p.Passed {
				continue
			}
			status := "[" + colorPass + "PASS" + colorReset + "]"
			if !p.Passed {
				status = "[" + colorFail + "FAIL" + colorReset + "]"
			}
			if p.NoStatements {
				fmt.Printf("    n/a  %s  %s\n", status, p.Package)
			} else {
				fmt.Printf("  %s  %s  %s\n", colorPct(ColorPct{Pct: p.Coverage}), status, p.Package)
			}
		}

		if covDetail == "file" && len(report.Files) > 0 {
			fmt.Println("\n==> File coverage:")
			for _, f := range report.Files {
				fmt.Printf("  %s  %s\n", colorPct(ColorPct{Pct: f.Coverage}), f.File)
			}
		}

		if covDetail == "func" && len(report.Funcs) > 0 {
			fmt.Println("\n==> Function coverage:")
			for _, f := range report.Funcs {
				fmt.Printf("  %s  %s:%d %s\n", colorPct(ColorPct{Pct: f.Coverage}), f.File, f.Line, f.Function)
			}
		}

		fmt.Printf("\n==> Total coverage: %s\n", colorPct(ColorPct{Pct: report.Total, Format: "%.1f%%"}))
	}

	// If tests failed, show failure details and return error
	if testErr != nil {
		if !jsonOutput && result.FailureOutput != "" {
			fmt.Println("\n==> Test failures:")
			fmt.Print(colorFail + result.FailureOutput + colorReset)
		}
		return fmt.Errorf("tests failed: %w", testErr)
	}

	// Handle --add-watermark: store watermark after coverage is computed
	if addWatermark {
		if err := gotest.SetWatermark(".", report.Total); err != nil {
			return fmt.Errorf("failed to set watermark: %w", err)
		}
		if !jsonOutput {
			fmt.Printf("\n==> Watermark set to %.1f%% (will be enforced on future runs)\n", report.Total)
		}
	}

	// Watermark enforcement: adjust threshold if watermark exists
	effectiveMin := minCoverage
	wm, wmExists, wmErr := gotest.GetWatermark(".")
	if wmErr != nil {
		return fmt.Errorf("failed to read watermark: %w", wmErr)
	}
	if wmExists {
		grace := wm - 2.5
		if grace < effectiveMin {
			effectiveMin = grace
		}
		if !jsonOutput {
			fmt.Printf("==> Watermark: %.1f%% (effective minimum: %.1f%%)\n", wm, effectiveMin)
		}
		// Ratchet up: update watermark if coverage improved
		if report.Total > wm {
			if err := gotest.SetWatermark(".", report.Total); err != nil {
				return fmt.Errorf("failed to update watermark: %w", err)
			}
			if !jsonOutput {
				fmt.Printf("==> Watermark updated: %.1f%% -> %.1f%%\n", wm, report.Total)
			}
		}
	}

	if report.Total < effectiveMin {
		if !jsonOutput {
			fmt.Println("\n==> Uncovered functions:")
			for _, f := range report.Funcs {
				if f.Coverage < 100 {
					fmt.Printf("  %s  %s:%d %s\n", colorPct(ColorPct{Pct: f.Coverage}), f.File, f.Line, f.Function)
				}
			}
		}
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
