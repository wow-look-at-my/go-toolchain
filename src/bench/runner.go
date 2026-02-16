package bench

import (
	"fmt"
	"io"

	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// Options configures benchmark execution
type Options struct {
	Time    string // -benchtime
	Count   int    // -count
	CPU     string // -cpu
	Verbose bool
}

// RunBenchmarks executes go test -bench and returns parsed results
func RunBenchmarks(r runner.CommandRunner, opts Options) (*BenchmarkReport, error) {
	goTestArgs := buildBenchArgs(opts)
	// Always run with -json so we can parse results
	goTestArgs = append([]string{goTestArgs[0], "-json"}, goTestArgs[1:]...)

	proc, err := runner.Cmd("go", goTestArgs...).WithQuiet().Run(r)
	if err != nil {
		return nil, fmt.Errorf("benchmarks failed: %w", err)
	}
	output, _ := io.ReadAll(proc.Stdout())
	waitErr := proc.Wait()

	if waitErr != nil {
		// Try to parse and return partial results on failure
		if len(output) > 0 {
			if report, parseErr := ParseBenchmarkOutput(output); parseErr == nil && report.HasResults() {
				return report, fmt.Errorf("benchmarks failed: %w", waitErr)
			}
		}
		return nil, fmt.Errorf("benchmarks failed: %w", waitErr)
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
