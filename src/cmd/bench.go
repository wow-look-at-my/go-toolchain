package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-toolchain/src/bench"
)

var (
	noBenchmark bool
	benchTime   string
	benchCount  int
	benchCPU    string
)

var benchCmd = &cobra.Command{
	Use:   "bench",
	Short: "Run and manage benchmarks",
	Long:  "Run benchmarks and compare against previous stored results.\n\nSubcommands: run, save, show, compare",
}

var benchRunCmd = &cobra.Command{
	Use:          "run",
	Short:        "Run benchmarks and show deltas vs stored results",
	SilenceUsage: true,
	RunE:         runBenchRun,
}

var benchSaveCmd = &cobra.Command{
	Use:          "save",
	Short:        "Run benchmarks and store results in git notes",
	SilenceUsage: true,
	RunE:         runBenchSave,
}

var benchShowCmd = &cobra.Command{
	Use:          "show [commit]",
	Short:        "Show stored benchmark results for a commit (default: HEAD)",
	SilenceUsage: true,
	RunE:         runBenchShow,
}

var benchCompareCmd = &cobra.Command{
	Use:          "compare <commit1> <commit2>",
	Short:        "Compare benchmark results between two commits",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(2),
	RunE:         runBenchCompare,
}

func init() {
	// Flags for run and save subcommands
	for _, cmd := range []*cobra.Command{benchRunCmd, benchSaveCmd} {
		cmd.Flags().StringVar(&benchTime, "benchtime", "", "Duration or count for each benchmark (e.g. 5s, 1000x)")
		cmd.Flags().IntVarP(&benchCount, "count", "n", 1, "Number of times to run each benchmark")
		cmd.Flags().StringVar(&benchCPU, "cpu", "", "GOMAXPROCS values to test with (comma-separated)")
	}

	benchCmd.AddCommand(benchRunCmd, benchSaveCmd, benchShowCmd, benchCompareCmd)
}

func runBenchRun(cmd *cobra.Command, args []string) error {
	runner := &RealCommandRunner{Quiet: jsonOutput}
	return runBenchRunWithRunner(runner)
}

func runBenchRunWithRunner(runner *RealCommandRunner) error {
	if !jsonOutput {
		fmt.Println("==> Running benchmarks")
	}

	opts := bench.Options{
		Time:    benchTime,
		Count:   benchCount,
		CPU:     benchCPU,
		Verbose: verbose,
	}

	report, err := bench.RunBenchmarks(runner, opts)
	if err != nil {
		if report != nil && report.HasResults() {
			report.Print()
		}
		return err
	}

	// Fetch previous results for comparison
	prev, prevSHA, _ := bench.FetchPrevious(runner)

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "\t")
		return enc.Encode(report)
	}

	if prev != nil && prevSHA != "" {
		fmt.Printf("\n==> Benchmark comparison vs %s\n", prevSHA)
		comp := bench.Compare(report, prev)
		comp.PreviousCommit = prevSHA
		comp.Print()
	} else {
		fmt.Println("\n==> Benchmark results (no previous data for comparison)")
		report.Print()
	}

	fmt.Println("==> Benchmarks complete")
	return nil
}

func runBenchSave(cmd *cobra.Command, args []string) error {
	runner := &RealCommandRunner{Quiet: jsonOutput}
	return runBenchSaveWithRunner(runner)
}

func runBenchSaveWithRunner(runner *RealCommandRunner) error {
	if !jsonOutput {
		fmt.Println("==> Running benchmarks")
	}

	opts := bench.Options{
		Time:    benchTime,
		Count:   benchCount,
		CPU:     benchCPU,
		Verbose: verbose,
	}

	report, err := bench.RunBenchmarks(runner, opts)
	if err != nil {
		if report != nil && report.HasResults() {
			report.Print()
		}
		return err
	}

	if !report.HasResults() {
		return fmt.Errorf("no benchmark results to save")
	}

	if err := bench.StoreNotes(runner, report); err != nil {
		return err
	}

	sha, _ := bench.GetHeadSHA(runner)
	if !jsonOutput {
		fmt.Printf("==> Benchmark results stored for %s\n", sha)
	}

	return nil
}

func runBenchShow(cmd *cobra.Command, args []string) error {
	runner := &RealCommandRunner{Quiet: jsonOutput}

	sha := "HEAD"
	if len(args) > 0 {
		sha = args[0]
	}

	report, err := bench.FetchForCommit(runner, sha)
	if err != nil {
		return err
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "\t")
		return enc.Encode(report)
	}

	fmt.Printf("==> Benchmark results for %s\n", sha)
	report.Print()
	return nil
}

func runBenchCompare(cmd *cobra.Command, args []string) error {
	runner := &RealCommandRunner{Quiet: jsonOutput}

	report1, err := bench.FetchForCommit(runner, args[0])
	if err != nil {
		return fmt.Errorf("commit %s: %w", args[0], err)
	}

	report2, err := bench.FetchForCommit(runner, args[1])
	if err != nil {
		return fmt.Errorf("commit %s: %w", args[1], err)
	}

	comp := bench.Compare(report2, report1)
	comp.PreviousCommit = args[0]

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "\t")
		return enc.Encode(comp)
	}

	fmt.Printf("==> Benchmark comparison: %s → %s\n", args[0], args[1])
	comp.Print()
	return nil
}

// benchRunner wraps CommandRunner to satisfy bench package interfaces
type benchRunner struct {
	CommandRunner
}

// runBenchmarkInBuild runs benchmarks as part of the default build
// and shows comparison against previous stored results
func runBenchmarkInBuild(runner CommandRunner) error {
	br := &benchRunner{runner}
	if !jsonOutput {
		fmt.Println("==> Running benchmarks")
	}

	opts := bench.Options{
		Time:    benchTime,
		Count:   benchCount,
		CPU:     benchCPU,
		Verbose: verbose,
	}

	report, err := bench.RunBenchmarks(br, opts)
	if err != nil {
		if report != nil && report.HasResults() {
			report.Print()
		}
		return err
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "\t")
		return enc.Encode(report)
	}

	// Fetch previous results for comparison
	prev, prevSHA, _ := bench.FetchPrevious(br)

	if prev != nil && prevSHA != "" {
		fmt.Printf("\n==> Benchmark comparison vs %s\n", prevSHA)
		comp := bench.Compare(report, prev)
		comp.PreviousCommit = prevSHA
		comp.Print()
	} else {
		fmt.Println()
		report.Print()
	}

	fmt.Println("==> Benchmarks complete")
	return nil
}
