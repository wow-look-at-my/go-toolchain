package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

type CoverageReport struct {
	Total    float32           `json:"total"`
	Packages []PackageCoverage `json:"packages"`
	Files    []FileCoverage    `json:"files,omitempty"`
	Funcs    []FuncCoverage    `json:"funcs,omitempty"`
}

type PackageCoverage struct {
	Package  string  `json:"package"`
	Coverage float32 `json:"coverage"`
	Passed   bool    `json:"passed"`
}

type FileCoverage struct {
	File       string  `json:"file"`
	Coverage   float32 `json:"coverage"`
	Statements int     `json:"statements"`
	Covered    int     `json:"covered"`
}

type FuncCoverage struct {
	File     string  `json:"file"`
	Line     int     `json:"line"`
	Function string  `json:"function"`
	Coverage float32 `json:"coverage"`
}

var (
	covDetail   string
	minCoverage float32
	output      string
	jsonOutput  bool
	verbose     bool
)

var rootCmd = &cobra.Command{
	Use:          "go-safe-build",
	Short:        "Build Go projects with coverage enforcement",
	Long:         "Runs go mod tidy, go test with coverage, and go build. Fails if coverage is below threshold.",
	SilenceUsage: true,
	RunE:         run,
}

func init() {
	rootCmd.Flags().StringVar(&covDetail, "cov-detail", "", "Show detailed coverage: 'func' or 'file'")
	rootCmd.Flags().Float32Var(&minCoverage, "min-coverage", 80.0, "Minimum coverage percentage (0 = test only, no build)")
	rootCmd.Flags().StringVarP(&output, "output", "o", "", "Output binary name (default: directory name)")
	rootCmd.Flags().BoolVar(&jsonOutput, "json", false, "Output coverage report as JSON")
	rootCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show test output line by line")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	runner := &RealCommandRunner{Quiet: jsonOutput}
	return runWithRunner(runner)
}

func runWithRunner(runner CommandRunner) error {
	testOnly := minCoverage == 0

	if testOnly && !jsonOutput {
		fmt.Println(warn("--min-coverage 0 runs tests ONLY. No binary will be produced."))
		fmt.Println(warn("This mode cannot bypass testing. To build, set --min-coverage > 0."))
		fmt.Println()
	}

	if !jsonOutput {
		fmt.Println("==> go mod tidy")
	}
	if err := runner.Run("go", "mod", "tidy"); err != nil {
		return fmt.Errorf("go mod tidy failed: %w", err)
	}

	if !jsonOutput {
		fmt.Println("==> go vet ./...")
	}
	if err := runner.Run("go", "vet", "./..."); err != nil {
		return fmt.Errorf("go vet failed: %w", err)
	}

	if !jsonOutput {
		fmt.Println("==> go test -json -coverprofile=coverage.out ./...")
	}

	result, testErr := runTests(runner, verbose)
	if result == nil {
		return fmt.Errorf("tests failed: %w", testErr)
	}

	report := &CoverageReport{
		Total:    result.Total,
		Packages: result.Packages,
	}

	if covDetail == "file" && len(result.Files) > 0 {
		report.Files = result.Files
	}

	if covDetail == "func" || jsonOutput {
		funcs, _ := parseFuncCoverage("coverage.out")
		report.Funcs = funcs
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "\t")
		if err := enc.Encode(report); err != nil {
			return fmt.Errorf("failed to encode JSON: %w", err)
		}
	} else {
		fmt.Println("\n==> Package coverage:")
		for _, p := range result.Packages {
			status := colorGreen + "PASS" + colorReset
			if !p.Passed {
				status = colorRed + "FAIL" + colorReset
			}
			fmt.Printf("  %s  [%s]  %s\n", colorPct(ColorPct{Pct: p.Coverage}), status, p.Package)
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

	// If tests failed, show failure details and return the error
	if testErr != nil {
		if !jsonOutput && result.FailureOutput != "" {
			fmt.Println("\n==> Test failures:")
			fmt.Print(colorFailure + result.FailureOutput + colorReset)
		}
		return fmt.Errorf("tests failed: %w", testErr)
	}

	// Test-only mode: report coverage and exit with error
	if testOnly {
		if !jsonOutput {
			fmt.Println("\n==> Test-only mode (--min-coverage 0), skipping build")
		}
		return fmt.Errorf("test-only mode: coverage is %.1f%%", report.Total)
	}

	if report.Total < minCoverage {
		if !jsonOutput {
			// Show uncovered functions to help identify what needs tests
			funcs, _ := parseFuncCoverage("coverage.out")
			fmt.Println("\n==> Uncovered functions:")
			for _, f := range funcs {
				if f.Coverage < 100 {
					fmt.Printf("  %s  %s:%d %s\n", colorPct(ColorPct{Pct: f.Coverage}), f.File, f.Line, f.Function)
				}
			}
		}
		return fmt.Errorf("coverage %.1f%% is below minimum %.1f%%", report.Total, minCoverage)
	}

	if output == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get working directory: %w", err)
		}
		output = filepath.Base(wd)
	}

	if !jsonOutput {
		fmt.Printf("==> go build -o %s ./...\n", output)
	}
	if err := runner.Run("go", "build", "-o", output, "./..."); err != nil {
		return fmt.Errorf("go build failed: %w", err)
	}

	if !jsonOutput {
		fmt.Println("==> Build successful")
	}
	return nil
}
