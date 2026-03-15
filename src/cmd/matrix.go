package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-toolchain/src/build"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"github.com/wow-look-at-my/go-toolchain/src/summary"
)

var (
	matrixOS      []string
	matrixArch    []string
	releaseParallel int
)

var (
	DefaultOS   = []string{"linux", "darwin", "windows"}
	DefaultArch = []string{"amd64", "arm64"}
)

func init() {
	matrixCmd := &cobra.Command{
		Use:          "matrix",
		Short:        "Cross-compile for multiple platforms",
		Long:         "Builds binaries for multiple GOOS/GOARCH combinations in parallel.",
		SilenceUsage: true,
		RunE:         runRelease,
	}
	matrixCmd.Flags().StringSliceVar(&matrixOS, "os", DefaultOS, "Target operating systems")
	matrixCmd.Flags().StringSliceVar(&matrixArch, "arch", DefaultArch, "Target architectures")
	matrixCmd.Flags().IntVarP(&releaseParallel, "parallel", "p", runtime.NumCPU(), "Number of parallel builds")
	matrixCmd.Flags().BoolVar(&noBenchmark, "no-benchmark", false, "Skip benchmarks after build")
	matrixCmd.Flags().StringVar(&benchTime, "benchtime", "", "Duration or count for each benchmark (e.g. 5s, 1000x)")
	matrixCmd.Flags().IntVarP(&benchCount, "count", "n", 1, "Number of times to run each benchmark")
	matrixCmd.Flags().StringVar(&benchCPU, "cpu", "", "GOMAXPROCS values to test with (comma-separated)")
	rootCmd.AddCommand(matrixCmd)
}

type buildJob struct {
	goos       string
	goarch     string
	srcPath    string
	outputPath string
	ldflags    string
}

type buildResult struct {
	job      buildJob
	err      error
	duration time.Duration
}

func runRelease(cmd *cobra.Command, args []string) error {
	r := runner.New()
	return runReleaseWithRunner(r)
}

func runReleaseWithRunner(r runner.CommandRunner) error {
	InitTimeline()
	setupCGOEnvironment()
	if len(matrixOS) == 0 || len(matrixArch) == 0 {
		return fmt.Errorf("no platforms specified (need at least one --os and one --arch)")
	}

	// Run tests with coverage first (same as default command)
	if _, _, err := RunTestsWithCoverage(r, false); err != nil {
		return err
	}

	// Resolve what to build
	targets, err := build.ResolveBuildTargets(r)
	if err != nil {
		return err
	}

	if len(targets) == 0 {
		return fmt.Errorf("no main packages found to build")
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}
	ensureBuildDirInGitignore()

	// Collect git info once for all builds
	info := collectGitInfo()
	ldflags := info.ldflags()

	// Build job queue - cartesian product of OS x Arch x Targets
	var jobs []buildJob
	for _, goos := range matrixOS {
		for _, goarch := range matrixArch {
			for _, target := range targets {
				outputName := build.BinaryName(target.OutputName, goos, goarch)
				jobs = append(jobs, buildJob{
					goos:       goos,
					goarch:     goarch,
					srcPath:    target.ImportPath,
					outputPath: filepath.Join(outputDir, outputName),
					ldflags:    ldflags,
				})
			}
		}
	}

	fmt.Printf("==> Building %d binaries (%d OS x %d arch)\n", len(jobs), len(matrixOS), len(matrixArch))
	buildStart := time.Now()

	// Run builds in parallel
	results := make(chan buildResult, len(jobs))
	jobChan := make(chan buildJob, len(jobs))

	var wg sync.WaitGroup
	workerCount := releaseParallel
	if workerCount > len(jobs) {
		workerCount = len(jobs)
	}

	for i := 0; i < workerCount; i++ {
		workerThread := fmt.Sprintf("worker-%d", i+1)
		wg.Add(1)
		go func(thread string) {
			defer wg.Done()
			for job := range jobChan {
				jobStart := time.Now()
				err := runBuild(r, job, nil)
				jobEnd := time.Now()
				if pipelineTimeline != nil {
					label := fmt.Sprintf("%s/%s", job.goos, job.goarch)
					pipelineTimeline.Record(label, thread, jobStart, jobEnd, err != nil)
				}
				results <- buildResult{job: job, err: err, duration: jobEnd.Sub(jobStart)}
			}
		}(workerThread)
	}

	for _, job := range jobs {
		jobChan <- job
	}
	close(jobChan)

	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	var failed []buildResult
	var builtFiles []string
	completed := 0
	for result := range results {
		completed++
		if result.err != nil {
			fmt.Printf("  FAIL [%d/%d] %s/%s: %v %s\n", completed, len(jobs), result.job.goos, result.job.goarch, result.err, fmtDuration(result.duration))
			failed = append(failed, result)
		} else {
			fmt.Printf("  OK   [%d/%d] %s %s\n", completed, len(jobs), result.job.outputPath, fmtDuration(result.duration))
			if _, statErr := os.Stat(result.job.outputPath); statErr == nil {
				builtFiles = append(builtFiles, result.job.outputPath)
			}
		}
	}

	if len(failed) > 0 {
		return fmt.Errorf("%d/%d builds failed", len(failed), len(jobs))
	}

	// Generate SHA-256 checksums for release artifacts
	if len(builtFiles) > 0 {
		if _, err := generateChecksums(outputDir, builtFiles); err != nil {
			return fmt.Errorf("checksum generation failed: %w", err)
		}
	}

	// Create _host and bare symlinks for the current platform
	if err := createHostSymlinks(targets, outputDir); err != nil {
		return err
	}

	fmt.Printf("==> All %d binaries built successfully in %s/ %s\n", len(jobs), outputDir, fmtDuration(time.Since(buildStart)))

	// Run benchmarks after successful build
	if !noBenchmark {
		if _, err := runBenchmarkInBuild(r); err != nil {
			return err
		}
	}

	// Write GitHub Step Summary with timeline
	if tl := GetTimeline(); tl != nil {
		sd := summary.SummaryData{Timeline: tl.Entries()}
		if writeErr := summary.Write(&sd); writeErr != nil {
			fmt.Fprintf(os.Stderr, "==> Warning: failed to write step summary: %v\n", writeErr)
		}
	}

	return nil
}

func createHostSymlinks(targets []build.Target, outDir string) error {
	hostOS := runtime.GOOS
	hostArch := runtime.GOARCH

	for _, target := range targets {
		hostBinary := build.BinaryName(target.OutputName, hostOS, hostArch)
		ext := ""
		if hostOS == "windows" {
			ext = ".exe"
		}

		// Verify the host binary exists in the output directory
		hostPath := filepath.Join(outDir, hostBinary)
		if _, err := os.Stat(hostPath); err != nil {
			fmt.Printf("  SKIP symlink for %s (host binary %s not found)\n", target.OutputName, hostBinary)
			continue
		}

		// Create <name>_host and <name> symlinks (relative, pointing to the host binary)
		for _, suffix := range []string{"_host", ""} {
			linkName := target.OutputName + suffix + ext
			linkPath := filepath.Join(outDir, linkName)
			os.Remove(linkPath) // remove any stale symlink
			if err := os.Symlink(hostBinary, linkPath); err != nil {
				return fmt.Errorf("failed to create symlink %s: %w", linkName, err)
			}
			fmt.Printf("  LINK %s -> %s\n", linkPath, hostBinary)
		}
	}
	return nil
}

// runBuild compiles a single binary. If onFirstOutput is non-nil, it is
// called when the compiler produces its first output (used for progress
// indicators on the default build path).
func runBuild(r runner.CommandRunner, job buildJob, onFirstOutput func()) error {
	cmd := runner.Cmd("go", "build", "-ldflags", job.ldflags, "-o", job.outputPath, job.srcPath)
	if job.goos != "" {
		cmd = cmd.WithEnv("GOOS", job.goos)
	}
	if job.goarch != "" {
		cmd = cmd.WithEnv("GOARCH", job.goarch)
	}
	if onFirstOutput != nil {
		cmd = cmd.WithOnFirstOutput(onFirstOutput)
	} else {
		cmd = cmd.WithQuiet()
	}
	if !cgoEnabled {
		cmd = cmd.WithEnv("CGO_ENABLED", "0")
	}
	proc, err := cmd.Run(r)
	if err != nil {
		return err
	}
	// Drain pipes before Wait to capture compiler errors and prevent deadlocks
	io.Copy(io.Discard, proc.Stdout())
	stderr, _ := io.ReadAll(proc.Stderr())
	if err := proc.Wait(); err != nil {
		if len(stderr) > 0 {
			return fmt.Errorf("%w\n%s", err, stderr)
		}
		return err
	}
	return nil
}

