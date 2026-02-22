package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-toolchain/src/build"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
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
	job buildJob
	err error
}

func runRelease(cmd *cobra.Command, args []string) error {
	r := runner.New()
	return runReleaseWithRunner(r)
}

func runReleaseWithRunner(r runner.CommandRunner) error {
	if len(matrixOS) == 0 || len(matrixArch) == 0 {
		return fmt.Errorf("no platforms specified (need at least one --os and one --arch)")
	}

	// Run tests with coverage first (same as default command)
	if _, err := RunTestsWithCoverage(r, false); err != nil {
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

	// Collect git info once for all builds
	info := collectGitInfo()
	ldflags := info.ldflags()

	// Build job queue - cartesian product of OS x Arch x Targets
	var jobs []buildJob
	for _, goos := range matrixOS {
		for _, goarch := range matrixArch {
			for _, target := range targets {
				ext := ""
				if goos == "windows" {
					ext = ".exe"
				}
				outputName := fmt.Sprintf("%s_%s_%s%s", target.OutputName, goos, goarch, ext)
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

	// Run builds in parallel
	results := make(chan buildResult, len(jobs))
	jobChan := make(chan buildJob, len(jobs))

	var wg sync.WaitGroup
	workerCount := releaseParallel
	if workerCount > len(jobs) {
		workerCount = len(jobs)
	}

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobChan {
				err := runBuild(r, job)
				results <- buildResult{job: job, err: err}
			}
		}()
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
	for result := range results {
		if result.err != nil {
			fmt.Printf("  FAIL %s/%s: %v\n", result.job.goos, result.job.goarch, result.err)
			failed = append(failed, result)
		} else {
			fmt.Printf("  OK   %s\n", result.job.outputPath)
		}
	}

	if len(failed) > 0 {
		return fmt.Errorf("%d/%d builds failed", len(failed), len(jobs))
	}

	fmt.Printf("==> All %d binaries built successfully in %s/\n", len(jobs), outputDir)
	return nil
}

func runBuild(r runner.CommandRunner, job buildJob) error {
	proc, err := runner.Cmd("go", "build", "-ldflags", job.ldflags, "-o", job.outputPath, job.srcPath).
		WithEnv("GOOS", job.goos).
		WithEnv("GOARCH", job.goarch).
		WithEnv("CGO_ENABLED", "0").
		WithQuiet().
		Run(r)
	if err != nil {
		return err
	}
	return proc.Wait()
}

