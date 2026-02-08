package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-toolchain/src/build"
)

var (
	releasePlatforms string
	releaseParallel  int
)

// DefaultPlatforms is the default set of GOOS/GOARCH combinations to build.
var DefaultPlatforms = []string{
	"linux/amd64",
	"linux/arm64",
	"darwin/amd64",
	"darwin/arm64",
	"windows/amd64",
}

func init() {
	matrixCmd := &cobra.Command{
		Use:          "matrix",
		Short:        "Cross-compile for multiple platforms",
		Long:         "Builds binaries for multiple GOOS/GOARCH combinations in parallel.",
		SilenceUsage: true,
		RunE:         runRelease,
	}
	matrixCmd.Flags().StringVar(&releasePlatforms, "platforms", strings.Join(DefaultPlatforms, ","), "Comma-separated list of GOOS/GOARCH pairs")
	matrixCmd.Flags().IntVarP(&releaseParallel, "parallel", "p", runtime.NumCPU(), "Number of parallel builds")
	rootCmd.AddCommand(matrixCmd)
}

type buildJob struct {
	goos       string
	goarch     string
	srcPath    string
	outputPath string
}

type buildResult struct {
	job buildJob
	err error
}

func runRelease(cmd *cobra.Command, args []string) error {
	runner := &RealCommandRunner{Quiet: false}
	return runReleaseWithRunner(runner)
}

func runReleaseWithRunner(runner CommandRunner) error {
	platforms := parsePlatforms(releasePlatforms)
	if len(platforms) == 0 {
		return fmt.Errorf("no valid platforms specified")
	}

	// Resolve what to build
	targets, err := build.ResolveBuildTargets(runner, srcPath)
	if err != nil {
		return err
	}

	if len(targets) == 0 {
		return fmt.Errorf("no main packages found to build")
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Build job queue
	var jobs []buildJob
	for _, platform := range platforms {
		parts := strings.Split(platform, "/")
		goos, goarch := parts[0], parts[1]

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
			})
		}
	}

	fmt.Printf("==> Building %d binaries across %d platforms\n", len(jobs), len(platforms))

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
				err := runBuild(runner, job)
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

func runBuild(runner CommandRunner, job buildJob) error {
	env := os.Environ()
	env = append(env, "GOOS="+job.goos, "GOARCH="+job.goarch, "CGO_ENABLED=0")

	return runner.RunWithEnv(env, "go", "build", "-o", job.outputPath, job.srcPath)
}

func parsePlatforms(input string) []string {
	var result []string
	for _, p := range strings.Split(input, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		parts := strings.Split(p, "/")
		if len(parts) != 2 {
			fmt.Printf("Warning: invalid platform %q (expected GOOS/GOARCH)\n", p)
			continue
		}
		result = append(result, p)
	}
	return result
}
