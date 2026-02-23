package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

var (
	profileOutput  string
	profileWeb     bool
	profileNoPprof bool
	profileType    string
)

var profileCmd = &cobra.Command{
	Use:   "profile [package]",
	Short: "Run benchmarks with CPU profiling",
	Long: `Run benchmarks with pprof CPU profiling enabled.

The profiler samples the call stack at regular intervals to identify
hot code paths. Output is written to a .pprof file that can be analyzed
with 'go tool pprof'.

Examples:
  go-toolchain profile                  # Profile and open pprof
  go-toolchain profile ./pkg/...        # Profile specific package
  go-toolchain profile --web            # Open pprof web UI instead
  go-toolchain profile --no-pprof       # Just write profile, don't open pprof`,
	SilenceUsage: true,
	RunE:         runProfile,
}

func init() {
	profileCmd.Flags().StringVarP(&profileOutput, "output", "o", "", "Output file (default: profile_cpu.pprof)")
	profileCmd.Flags().BoolVar(&profileWeb, "web", false, "Open pprof web UI instead of interactive mode")
	profileCmd.Flags().BoolVar(&profileNoPprof, "no-pprof", false, "Don't open pprof after profiling")
	profileCmd.Flags().StringVar(&profileType, "type", "cpu", "Profile type: cpu, mem, mutex, block")
	profileCmd.Flags().StringVar(&benchTime, "benchtime", "3s", "Duration for each benchmark")
	profileCmd.Flags().IntVarP(&benchCount, "count", "n", 1, "Number of times to run each benchmark")
	rootCmd.AddCommand(profileCmd)
}

func runProfile(cmd *cobra.Command, args []string) error {
	r := runner.New()
	return runProfileWithRunner(r, args)
}

func runProfileWithRunner(r runner.CommandRunner, args []string) error {
	// Determine output file
	outFile := profileOutput
	if outFile == "" {
		outFile = fmt.Sprintf("profile_%s.pprof", profileType)
	}

	// Ensure absolute path for profile output
	absOut, err := filepath.Abs(outFile)
	if err != nil {
		return fmt.Errorf("failed to resolve output path: %w", err)
	}

	// Build go test args for profiling
	goTestArgs := []string{"test", "-run", "^$", "-bench", "."}

	// Add profile flag based on type
	switch profileType {
	case "cpu":
		goTestArgs = append(goTestArgs, "-cpuprofile", absOut)
	case "mem":
		goTestArgs = append(goTestArgs, "-memprofile", absOut)
	case "mutex":
		goTestArgs = append(goTestArgs, "-mutexprofile", absOut)
	case "block":
		goTestArgs = append(goTestArgs, "-blockprofile", absOut)
	default:
		return fmt.Errorf("unknown profile type %q (use: cpu, mem, mutex, block)", profileType)
	}

	if benchTime != "" {
		goTestArgs = append(goTestArgs, "-benchtime", benchTime)
	}
	if benchCount > 1 {
		goTestArgs = append(goTestArgs, "-count", fmt.Sprintf("%d", benchCount))
	}

	// Package target
	target := "./..."
	if len(args) > 0 {
		target = args[0]
	}
	goTestArgs = append(goTestArgs, target)

	fmt.Printf("==> Running %s profiling on %s\n", profileType, target)
	fmt.Printf("==> Output: %s\n", absOut)

	proc, err := runner.Cmd("go", goTestArgs...).Run(r)
	if err != nil {
		return fmt.Errorf("profiling failed: %w", err)
	}
	if err := proc.Wait(); err != nil {
		return fmt.Errorf("profiling failed: %w", err)
	}

	// Verify output was written
	if _, statErr := os.Stat(absOut); statErr != nil {
		return fmt.Errorf("profile output not created (no benchmarks found?)")
	}

	fmt.Printf("==> Profile written to %s\n", absOut)

	if profileNoPprof {
		fmt.Printf("\nAnalyze with:\n")
		fmt.Printf("  go tool pprof %s\n", absOut)
		fmt.Printf("  go tool pprof -http=: %s\n", absOut)
		return nil
	}

	if profileWeb {
		fmt.Println("==> Opening pprof web UI...")
		pprofCmd := exec.Command("go", "tool", "pprof", "-http=:", absOut)
		pprofCmd.Stdout = os.Stdout
		pprofCmd.Stderr = os.Stderr
		if err := pprofCmd.Start(); err != nil {
			return fmt.Errorf("failed to start pprof: %w", err)
		}
		fmt.Printf("==> pprof running (PID %d)\n", pprofCmd.Process.Pid)
		return nil
	}

	// Default: run pprof interactively
	fmt.Println("==> Opening pprof...")
	pprofCmd := exec.Command("go", "tool", "pprof", absOut)
	pprofCmd.Stdin = os.Stdin
	pprofCmd.Stdout = os.Stdout
	pprofCmd.Stderr = os.Stderr
	return pprofCmd.Run()
}
