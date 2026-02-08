package cmd

import (
	"encoding/json"
	"fmt"
	"os"
)

var (
	doBenchmark bool
	benchTime   string
	benchRuns   int
	benchCPUs   string
)

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
	goTestArgs := []string{"test", "-run", "^$", "-bench", ".", "-benchmem"}
	if benchTime != "" {
		goTestArgs = append(goTestArgs, "-benchtime", benchTime)
	}
	if benchRuns > 1 {
		goTestArgs = append(goTestArgs, "-count", fmt.Sprintf("%d", benchRuns))
	}
	if benchCPUs != "" {
		goTestArgs = append(goTestArgs, "-cpu", benchCPUs)
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
