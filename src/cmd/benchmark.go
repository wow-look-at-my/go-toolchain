package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	benchTime  string
	benchCount int
	benchCPU   string
	benchMem   bool
)

func init() {
	benchCmd := &cobra.Command{
		Use:          "benchmark",
		Short:        "Run Go benchmarks",
		Long:         "Runs all Go benchmarks (go test -bench .) in the current directory and all subdirectories.",
		SilenceUsage: true,
		RunE:         runBenchmark,
	}
	benchCmd.Flags().StringVar(&benchTime, "benchtime", "", "Duration or count for each benchmark (e.g. 5s, 1000x)")
	benchCmd.Flags().IntVarP(&benchCount, "count", "n", 1, "Number of times to run each benchmark")
	benchCmd.Flags().StringVar(&benchCPU, "cpu", "", "GOMAXPROCS values to test with (comma-separated, e.g. 1,2,4)")
	benchCmd.Flags().BoolVar(&benchMem, "benchmem", true, "Print memory allocation statistics")
	rootCmd.AddCommand(benchCmd)
}

func runBenchmark(cmd *cobra.Command, args []string) error {
	runner := &RealCommandRunner{Quiet: jsonOutput}
	return runBenchmarkWithRunner(runner)
}

func runBenchmarkWithRunner(runner CommandRunner) error {
	goTestArgs := buildBenchArgs()

	if jsonOutput {
		return runBenchmarkJSON(runner, goTestArgs)
	}

	fmt.Println("==> Running benchmarks")
	if err := runner.Run("go", goTestArgs...); err != nil {
		return fmt.Errorf("benchmarks failed: %w", err)
	}
	fmt.Println("==> Benchmarks complete")
	return nil
}

func buildBenchArgs() []string {
	goTestArgs := []string{"test", "-run", "^$", "-bench", "."}

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
	goTestArgs = append(goTestArgs, "./...")
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
