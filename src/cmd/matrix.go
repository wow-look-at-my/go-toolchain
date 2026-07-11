package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-toolchain/src/build"
	"github.com/wow-look-at-my/go-toolchain/src/codeql"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/profile"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"github.com/wow-look-at-my/go-toolchain/src/summary"
	gotrace "github.com/wow-look-at-my/go-toolchain/src/trace"
)

var (
	matrixOS        []string
	matrixArch      []string
	matrixTargets   []string
	cosmoSlots      []string
	releaseParallel int
)

var (
	DefaultOS   = []string{"linux", "darwin", "windows"}
	DefaultArch = []string{"amd64", "arm64"}
)

func init() {
	matrixCmd := &cobra.Command{
		Use:   "matrix",
		Short: "Cross-compile for multiple platforms",
		Long: `Builds binaries for multiple GOOS/GOARCH combinations in parallel.

Targets are the cartesian product of --os and --arch, unless --targets is set,
in which case exactly the listed targets are built. Each --targets entry is an
os/arch pair (e.g. darwin/amd64) or the special value "cosmo": one fat
Actually Portable Executable built with the gosmopolitan Go fork, covering
Linux, macOS and Windows in a single binary (artifact <name>_cosmo_fat). After
a cosmo build the fat APE is also copied to the per-platform artifact names
listed in --cosmo-slots, so per-platform consumers keep working; an explicit
native target in --targets wins over a slot copy of the same name.`,
		SilenceUsage: true,
		RunE:         runRelease,
	}
	addMatrixTargetFlags(matrixCmd)
	matrixCmd.Flags().IntVarP(&releaseParallel, "parallel", "p", runtime.NumCPU(), "Number of parallel builds")
	matrixCmd.Flags().BoolVar(&noBenchmark, "no-benchmark", false, "Skip benchmarks after build")
	matrixCmd.Flags().StringVar(&benchTime, "benchtime", "", "Duration or count for each benchmark (e.g. 5s, 1000x)")
	matrixCmd.Flags().IntVarP(&benchCount, "count", "n", 1, "Number of times to run each benchmark")
	matrixCmd.Flags().StringVar(&benchCPU, "cpu", "", "GOMAXPROCS values to test with (comma-separated)")
	rootCmd.AddCommand(matrixCmd)
}

// addMatrixTargetFlags registers the target-selection flags shared by the
// matrix command and release --build.
func addMatrixTargetFlags(cmd *cobra.Command) {
	cmd.Flags().StringSliceVar(&matrixOS, "os", DefaultOS, "Target operating systems")
	cmd.Flags().StringSliceVar(&matrixArch, "arch", DefaultArch, "Target architectures")
	cmd.Flags().StringSliceVar(&matrixTargets, "targets", nil, `Exact build targets as os/arch pairs plus the special value "cosmo" (a gosmopolitan fat APE); replaces the --os x --arch product`)
	cmd.Flags().StringSliceVar(&cosmoSlots, "cosmo-slots", DefaultCosmoSlots, `Per-platform artifact names that receive a copy of the cosmo fat APE ("none" disables slot mapping)`)
}

type buildJob struct {
	goos       string
	goarch     string
	srcPath    string
	outputPath string
	ldflags    string
	// cosmoGoroot is the gosmopolitan toolchain GOROOT for GOOS=cosmo fat-APE
	// jobs; empty for normal jobs, which build with the go on PATH.
	cosmoGoroot string
}

type buildResult struct {
	job      buildJob
	err      error
	duration time.Duration
}

func runRelease(cmd *cobra.Command, args []string) error {
	InitTimeline()
	// Collect per-action build profiles for every cross-compile target. The
	// matrix path has no Chrome trace, but the deferred capture still parses
	// and stashes the graphs so printCacheStats can emit the final report.
	initBuildProfile()
	defer captureProfileTrace()
	r := runner.New()
	err := runReleaseWithRunner(r)
	if err != nil {
		return err
	}

	maybeSubmitDeps()

	// Write GitHub Step Summary with timeline
	if tl := GetTimeline(); tl != nil {
		sd := summary.SummaryData{Timeline: tl.Entries()}
		if writeErr := summary.Write(&sd); writeErr != nil {
			fmt.Fprintf(os.Stderr, "⇒ Warning: failed to write step summary: %v\n", writeErr)
		}

		// Export OTel traces (no-op if OTEL_EXPORTER_OTLP_ENDPOINT is unset).
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := gotrace.Export(ctx, sd.Timeline); err != nil {
			fmt.Fprintf(os.Stderr, "⇒ Warning: failed to export traces: %v\n", err)
		}
	}
	return nil
}

func runReleaseWithRunner(r runner.CommandRunner) error {
	setupCGOEnvironment()
	platforms, err := resolveMatrixPlatforms()
	if err != nil {
		return err
	}

	// Resolve the cosmo prerequisites up front so a missing gosmopolitan
	// toolchain or a bad --cosmo-slots value fails fast, before the test phase.
	var cosmoGoroot string
	var slotPlatforms []buildPlatform
	if slices.ContainsFunc(platforms, buildPlatform.IsCosmo) {
		slotPlatforms, err = parseCosmoSlots(cosmoSlots)
		if err != nil {
			return err
		}
		if cgoEnabled {
			fmt.Fprintf(os.Stderr, "⇒ Warning: --cgo has no effect on the cosmo target (cosmopolitan has no cgo; CGO_ENABLED=0 is forced)\n")
		}
		if cosmoGoroot, err = ensureCosmoToolchainFunc(); err != nil {
			return err
		}
	}

	// Run tests with coverage first (same as default command)
	if _, _, err := RunTestsWithCoverage(r, false); err != nil {
		return err
	}

	if codeql.Enabled() {
		ex := logStep("codeql extract")
		if err := codeql.Extract(r); err != nil {
			ex.failed()
			return err
		}
		ex.done()
	}

	// Validate the working tree before go-toolchain writes any of its own
	// build-time artifacts (the transient guard, the build/ dir, .gitignore
	// upkeep), so those generated files never fail the dirty-tree check.
	if err := checkDirtyInCI(); err != nil {
		return err
	}

	// Inject the GOMEMLIMIT guard into each main package so the cross-compiled
	// binaries cap the Go heap at the cgroup limit too, then remove the transient
	// guards once every platform has been built.
	if err := injectMemLimitGuard(false); err != nil {
		return err
	}
	defer cleanupMemLimitGuards()

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

	// Build job queue - one job per platform per main package
	var jobs []buildJob
	for _, p := range platforms {
		for _, target := range targets {
			outputName := build.BinaryName(target.OutputName, p.OS, p.Arch)
			job := buildJob{
				goos:       p.OS,
				goarch:     p.Arch,
				srcPath:    target.ImportPath,
				outputPath: filepath.Join(outputDir, outputName),
			}
			if p.IsCosmo() {
				job.cosmoGoroot = cosmoGoroot
				// A previous local run leaves <name>_cosmo_fat as a symlink to
				// a slot copy (see copyCosmoSlots). Remove it before building:
				// `go build -o` follows symlinks, so it would otherwise write
				// the new APE THROUGH the link into the slot artifact and the
				// slot mapping would then copy the file onto itself.
				if err := os.Remove(job.outputPath); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("removing stale %s: %w", job.outputPath, err)
				}
			}
			jobs = append(jobs, job)
		}
	}

	if len(matrixTargets) > 0 {
		fmt.Printf("⇒ Building %d binaries (%d targets)\n", len(jobs), len(platforms))
	} else {
		fmt.Printf("⇒ Building %d binaries (%d OS x %d arch)\n", len(jobs), len(matrixOS), len(matrixArch))
	}
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

	// Copy the cosmo fat APE onto its conventional per-platform artifact
	// names so per-platform consumers (buildhost slots) keep resolving. Runs
	// before checksum generation so the copies are covered too. In CI the fat
	// name is then dropped (buildhost rejects os=cosmo uploads and
	// upload-artifact dereferences symlinks); locally it becomes a symlink to
	// the first slot copy. Replaced fat paths leave builtFiles: checksums
	// cover real files only.
	if cosmoGoroot != "" && len(slotPlatforms) > 0 {
		nativeBuilt := make(map[string]bool)
		for _, job := range jobs {
			if job.cosmoGoroot == "" {
				nativeBuilt[filepath.Base(job.outputPath)] = true
			}
		}
		copied, replacedFat, err := copyCosmoSlots(targets, outputDir, slotPlatforms, nativeBuilt, os.Getenv("CI") != "")
		if err != nil {
			return err
		}
		builtFiles = append(builtFiles, copied...)
		for _, fat := range replacedFat {
			builtFiles = slices.DeleteFunc(builtFiles, func(p string) bool { return p == fat })
		}
	}

	// Generate SHA-256 checksums for release artifacts
	if len(builtFiles) > 0 {
		if _, err := generateChecksums(outputDir, builtFiles); err != nil {
			return fmt.Errorf("checksum generation failed: %w", err)
		}
	}

	// Create _host and bare symlinks for the current platform. In CI these
	// are pointless (nothing consumes them) and harmful: upload-artifact
	// dereferences symlinks, bloating the artifact with full duplicate copies.
	if os.Getenv("CI") == "" {
		if err := createHostSymlinks(targets, outputDir); err != nil {
			return err
		}
	}

	fmt.Printf("⇒ All %d binaries built successfully in %s/ %s\n", len(jobs), outputDir, fmtDuration(time.Since(buildStart)))

	// Run benchmarks after successful build
	if !noBenchmark {
		if _, err := runBenchmarkInBuild(r); err != nil {
			return err
		}
	}

	return nil
}

func createHostSymlinks(targets []build.Target, outDir string) error {
	// hostos, not runtime: the symlink must point at the matrix binary built
	// for the OS this process is running on, and a cosmo fat APE reports
	// runtime.GOOS=="cosmo" everywhere. runtime.GOARCH matches the host.
	hostOS := hostos.GOOS()
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
	args := []string{"build"}
	// Dump the action graph for the build profile (one file per invocation;
	// matrix targets each get their own). No-op when profiling is off.
	if garg := profile.GraphArg(); garg != "" {
		args = append(args, garg)
	}
	if onFirstOutput != nil {
		args = append(args, "-v") // print packages as they are compiled
	}
	if job.ldflags != "" {
		args = append(args, "-ldflags", job.ldflags)
	}
	args = append(args, "-o", job.outputPath, job.srcPath)
	goCmd := "go"
	if job.cosmoGoroot != "" {
		goCmd = filepath.Join(job.cosmoGoroot, "bin", "go")
	}
	cmd := runner.Cmd(goCmd, args...)
	if job.cosmoGoroot != "" {
		// GOOS=cosmo fat-APE build via the gosmopolitan toolchain. GOARCH is
		// cleared: fat (amd64+arm64+windows payloads in one output) is the
		// fork's default and the job's pseudo-arch "fat" is a naming artifact,
		// not a GOARCH. GOCOSMOFAT is cleared too so an inherited =0 cannot
		// silently produce a thin binary that the slot copies would mislabel.
		// CGO_ENABLED=0 always: cosmopolitan has no cgo.
		cmd = cmd.WithEnv("GOOS", cosmoOS).
			WithEnv("GOARCH", "").
			WithEnv("GOCOSMOFAT", "").
			WithEnv("GOTOOLCHAIN", "local").
			WithEnv("GOROOT", job.cosmoGoroot).
			WithEnv("PATH", filepath.Join(job.cosmoGoroot, "bin")+string(os.PathListSeparator)+os.Getenv("PATH")).
			WithEnv("CGO_ENABLED", "0")
	} else {
		if job.goos != "" {
			cmd = cmd.WithEnv("GOOS", job.goos)
		}
		if job.goarch != "" {
			cmd = cmd.WithEnv("GOARCH", job.goarch)
		}
	}
	if onFirstOutput != nil {
		cmd = cmd.WithOnFirstOutput(onFirstOutput)
		if activeMissTracker != nil {
			cmd = cmd.WithStderrWriter(activeMissTracker)
		}
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
	if onFirstOutput != nil {
		// Non-quiet: let Wait() stream -v output to console in real-time.
		// Compiler errors are printed to stderr as they occur.
		return proc.Wait()
	}
	// Quiet (matrix): drain pipes manually, capture stderr for error messages
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
