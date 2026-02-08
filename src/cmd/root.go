package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"go-safe-build/src/build"
	gotest "go-safe-build/src/test"
)

var (
	covDetail     string
	minCoverage   float32
	outputDir     string
	srcPath       string
	jsonOutput    bool
	verbose       bool
	addWatermark  bool
	doRemoveWmark bool
)

var rootCmd = &cobra.Command{
	Use:          "go-safe-build",
	Short:        "Build Go projects with coverage enforcement",
	SilenceUsage: true,
	RunE:         run,
}

func init() {
	rootCmd.Long = "Runs go mod tidy, go test with coverage, and go build. Fails if coverage is below threshold.\n\n" + installStatus()
	rootCmd.Flags().StringVar(&covDetail, "cov-detail", "", "Show detailed coverage: 'func' or 'file'")
	rootCmd.Flags().Float32Var(&minCoverage, "min-coverage", 80.0, "Minimum coverage percentage (0 = test only, no build)")
	rootCmd.Flags().StringVarP(&outputDir, "output", "o", "build", "Output directory for binaries")
	rootCmd.Flags().StringVar(&srcPath, "src", ".", "Path to main package (e.g., ./cmd/myapp)")
	rootCmd.Flags().BoolVar(&jsonOutput, "json", false, "Output coverage report as JSON")
	rootCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show test output line by line")
	rootCmd.Flags().BoolVar(&addWatermark, "add-watermark", false, "Store current coverage as watermark (enforced on future runs)")
	rootCmd.Flags().BoolVar(&doRemoveWmark, "remove-watermark", false, "Remove the coverage watermark")
	rootCmd.Flags().MarkHidden("remove-watermark")
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

	if !jsonOutput {
		fmt.Println("==> go mod tidy")
	}
	if err := runner.Run("go", "mod", "tidy"); err != nil {
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

	tmpDir, err := os.MkdirTemp("", "go-safe-build-*")
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

	// If tests failed, show failures and bail — coverage is irrelevant
	if testErr != nil {
		if jsonOutput {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "\t")
			enc.Encode(report)
		} else if result.FailureOutput != "" {
			fmt.Println("\n==> Test failures:")
			fmt.Print(colorFailure + result.FailureOutput + colorReset)
		}
		return fmt.Errorf("tests failed: %w", testErr)
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "\t")
		if err := enc.Encode(report); err != nil {
			return fmt.Errorf("failed to encode JSON: %w", err)
		}
	} else {
		fmt.Println("\n==> Package coverage:")
		for _, p := range report.Packages {
			fmt.Printf("  %s  %s\n", colorPct(ColorPct{Pct: p.Coverage}), p.Package)
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

	// Test-only mode: report coverage and exit with error
	if testOnly {
		if !jsonOutput {
			fmt.Println("\n==> Test-only mode (--min-coverage 0), skipping build")
		}
		return fmt.Errorf("test-only mode: coverage is %.1f%%", report.Total)
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

	targets, err := build.ResolveBuildTargets(runner, srcPath)
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
		for _, t := range targets {
			outPath := filepath.Join(outputDir, t.OutputName)
			if !jsonOutput {
				fmt.Printf("==> go build -o %s %s\n", outPath, t.ImportPath)
			}
			if err := runner.Run("go", "build", "-o", outPath, t.ImportPath); err != nil {
				return fmt.Errorf("go build failed: %w", err)
			}
		}
	}

	if !jsonOutput {
		fmt.Println("==> Build successful")
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
