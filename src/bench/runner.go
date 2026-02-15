package bench

import (
	"fmt"
)

// CommandRunner abstracts command execution for testing
type CommandRunner interface {
	RunWithOutput(name string, args ...string) ([]byte, error)
}

// Options configures benchmark execution
type Options struct {
	Time    string // -benchtime
	Count   int    // -count
	CPU     string // -cpu
	Verbose bool
}

// RunBenchmarks executes go test -bench and returns parsed results
func RunBenchmarks(runner CommandRunner, opts Options) (*BenchmarkReport, error) {
	goTestArgs := buildBenchArgs(opts)
	// Always run with -json so we can parse results
	goTestArgs = append([]string{goTestArgs[0], "-json"}, goTestArgs[1:]...)

	output, err := runner.RunWithOutput("go", goTestArgs...)
	if err != nil {
		// Try to parse and return partial results on failure
		if len(output) > 0 {
			if report, parseErr := ParseBenchmarkOutput(output); parseErr == nil && report.HasResults() {
				return report, fmt.Errorf("benchmarks failed: %w", err)
			}
		}
		return nil, fmt.Errorf("benchmarks failed: %w", err)
	}

	report, err := ParseBenchmarkOutput(output)
	if err != nil {
		return nil, fmt.Errorf("failed to parse benchmark output: %w", err)
	}

	return report, nil
}

func buildBenchArgs(opts Options) []string {
	goTestArgs := []string{"test", "-run", "^$", "-bench", ".", "-benchmem"}
	if opts.Time != "" {
		goTestArgs = append(goTestArgs, "-benchtime", opts.Time)
	}
	if opts.Count > 1 {
		goTestArgs = append(goTestArgs, "-count", fmt.Sprintf("%d", opts.Count))
	}
	if opts.CPU != "" {
		goTestArgs = append(goTestArgs, "-cpu", opts.CPU)
	}
	if opts.Verbose {
		goTestArgs = append(goTestArgs, "-v")
	}
	goTestArgs = append(goTestArgs, "./...")
	return goTestArgs
}
