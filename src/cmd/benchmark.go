package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var (
	benchPattern string
	benchTime    string
	benchCount   int
	benchCPU     string
	benchMem     bool
)

func init() {
	benchCmd := &cobra.Command{
		Use:          "benchmark [packages]",
		Short:        "Run Go benchmarks",
		Long:         "Runs go test -bench with configurable patterns, iterations, and output options.",
		SilenceUsage: true,
		RunE:         runBenchmark,
	}
	benchCmd.Flags().StringVarP(&benchPattern, "bench", "b", ".", "Benchmark pattern to match (passed to -bench)")
	benchCmd.Flags().StringVar(&benchTime, "benchtime", "", "Duration or count for each benchmark (e.g. 5s, 1000x)")
	benchCmd.Flags().IntVarP(&benchCount, "count", "n", 1, "Number of times to run each benchmark")
	benchCmd.Flags().StringVar(&benchCPU, "cpu", "", "GOMAXPROCS values to test with (comma-separated, e.g. 1,2,4)")
	benchCmd.Flags().BoolVar(&benchMem, "benchmem", true, "Print memory allocation statistics")
	rootCmd.AddCommand(benchCmd)
}

func runBenchmark(cmd *cobra.Command, args []string) error {
	runner := &RealCommandRunner{Quiet: jsonOutput}
	return runBenchmarkWithRunner(runner, args)
}

func runBenchmarkWithRunner(runner CommandRunner, args []string) error {
	packages := "./..."
	if len(args) > 0 {
		packages = strings.Join(args, " ")
	}

	goTestArgs := buildBenchArgs(packages)

	if jsonOutput {
		return runBenchmarkJSON(runner, goTestArgs)
	}

	fmt.Printf("==> Running benchmarks (pattern: %s)\n", benchPattern)
	if err := runner.Run("go", goTestArgs...); err != nil {
		return fmt.Errorf("benchmarks failed: %w", err)
	}
	fmt.Println("==> Benchmarks complete")
	return nil
}

func buildBenchArgs(packages string) []string {
	goTestArgs := []string{"test", "-run", "^$", "-bench", benchPattern}

	if benchMem {
		goTestArgs = append(goTestArgs, "-benchmem")
	}
	if benchTime != "" {
		goTestArgs = append(goTestArgs, "-benchtime", benchTime)
	}
	if benchCount > 1 {
		goTestArgs = append(goTestArgs, "-count", fmt.Sprintf("%d", benchCount))
	}
	if benchCPU != "" {
		goTestArgs = append(goTestArgs, "-cpu", benchCPU)
	}
	if verbose {
		goTestArgs = append(goTestArgs, "-v")
	}
	goTestArgs = append(goTestArgs, packages)
	return goTestArgs
}

func runBenchmarkJSON(runner CommandRunner, goTestArgs []string) error {
	goTestArgs = append([]string{goTestArgs[0], "-json"}, goTestArgs[1:]...)
	output, err := runner.RunWithOutput("go", goTestArgs...)
	if err != nil {
		// Still try to output what we got
		if len(output) > 0 {
			os.Stdout.Write(output)
		}
		return fmt.Errorf("benchmarks failed: %w", err)
	}

	// Parse and re-emit as a JSON object with a "raw" field
	result := struct {
		Raw string `json:"raw"`
	}{
		Raw: string(output),
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "\t")
	return enc.Encode(result)
}
