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
	"github.com/wow-look-at-my/go-toolchain/src/cache"
	"github.com/wow-look-at-my/go-toolchain/src/codeql"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/profile"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"github.com/wow-look-at-my/go-toolchain/src/summary"
	gotrace "github.com/wow-look-at-my/go-toolchain/src/trace"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
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
native target in --targets wins over a slot copy of the same name.

The WebAssembly targets wasm/js (browser/Node.js) and wasm/wasip1 (WASI) are
also built with the gosmopolitan fork toolchain (it carries the org's wasm
runtime fixes); the GOOS-order spellings js/wasm and wasip1/wasm are accepted
as compatibility aliases for the same targets, and the cartesian flags accept
the pairing too (--os wasm combines only with --arch js/wasip1 and yields the
identical targets). Their artifacts use
buildhost's publishable wasm naming (<name>_wasm_js, <name>_wasm_wasip1 —
os=wasm with arch=js/wasip1, no file extension); publishing them requires a
buildhost with wasm artifact support. Set GO_TOOLCHAIN_WASM_PUBLISH=0 to use
the excluded <name>_<goos>_wasm.wasm naming instead, which never reaches the
buildhost publish upload set.`,
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
	cmd.Flags().StringSliceVar(&matrixTargets, "targets", nil, `Exact build targets as os/arch pairs (incl. wasm/js and wasm/wasip1, built with the gosmopolitan toolchain) plus the special value "cosmo" (a gosmopolitan fat APE); replaces the --os x --arch product`)
	cmd.Flags().StringSliceVar(&cosmoSlots, "cosmo-slots", DefaultCosmoSlots, `Per-platform artifact names that receive a copy of the cosmo fat APE ("none" disables slot mapping)`)
}

type buildJob struct {
	goos       string
	goarch     string
	srcPath    string
	outputPath string
	ldflags    string
	// forkGoroot is the gosmopolitan toolchain GOROOT for jobs built with the
	// fork: GOOS=cosmo fat-APE jobs and wasm (js/wasm, wasip1/wasm) jobs.
	// Empty for normal jobs, which build with the go on PATH.
	forkGoroot string
	// cacheNamespace is the cache key namespace for fork-toolchain jobs — a
	// content hash of the toolchain at forkGoroot (forkToolchainCacheNamespace),
	// exported to the build as GO_TOOLCHAIN_CACHE_NAMESPACE so its cacheprog
	// scopes every cache key to this exact toolchain build. REQUIRED whenever
	// forkGoroot is set (runBuild refuses a fork job without it): an
	// un-namespaced fork build would share action keys with other fork
	// toolchain builds and reopen cross-build cache poisoning. Empty for
	// normal jobs, whose toolchains have properly version-keyed tool IDs.
	cacheNamespace string
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
			logger.Warn("⇒ Warning: failed to write step summary: %v", writeErr)
		}

		// Export OTel traces (no-op if OTEL_EXPORTER_OTLP_ENDPOINT is unset).
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := gotrace.Export(ctx, sd.Timeline); err != nil {
			logger.Warn("⇒ Warning: failed to export traces: %v", err)
		}
	}

	// Warnings budget: fail the run — after every phase has completed and
	// every warning has been printed — when it emitted more than maxWarnings
	// warnings (same gate as the default pipeline).
	return checkWarningsGate()
}

func runReleaseWithRunner(r runner.CommandRunner) error {
	setupCGOEnvironment()
	platforms, err := resolveMatrixPlatforms()
	if err != nil {
		return err
	}

	// Resolve the gosmopolitan-fork prerequisites up front so a missing
	// toolchain or a bad --cosmo-slots value fails fast, before the test
	// phase. Both the cosmo fat APE and the wasm targets build with the fork.
	hasCosmo := slices.ContainsFunc(platforms, buildPlatform.IsCosmo)
	hasWasm := slices.ContainsFunc(platforms, buildPlatform.IsWasm)
	var forkGoroot string
	var slotPlatforms []buildPlatform
	if hasCosmo {
		slotPlatforms, err = parseCosmoSlots(cosmoSlots)
		if err != nil {
			return err
		}
		if cgoEnabled {
			logger.Warn("⇒ Warning: --cgo has no effect on the cosmo target (cosmopolitan has no cgo; CGO_ENABLED=0 is forced)")
		}
	}
	if hasWasm && cgoEnabled {
		logger.Warn("⇒ Warning: --cgo has no effect on wasm targets (WebAssembly has no cgo; CGO_ENABLED=0 is forced)")
	}
	var forkCacheNamespace string
	if hasCosmo || hasWasm {
		if forkGoroot, err = ensureCosmoToolchainFunc(); err != nil {
			return err
		}
		// Fingerprint the fork toolchain for cache isolation: every fork job's
		// cacheprog scopes its cache keys to this content hash, so two
		// different fork toolchain builds can never share cache entries even
		// though the fork's constant version stamp gives them colliding action
		// IDs (the 2026-07-20 cross-build poisoning — SIGSEGV APEs built from
		// stale shared-cache objects). Fail closed: a fork build without a
		// namespace would ride the shared cache un-isolated.
		if forkCacheNamespace, err = forkToolchainCacheNamespace(forkGoroot); err != nil {
			return fmt.Errorf("fingerprinting the fork toolchain for cache isolation: %w", err)
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

	// Resolve what to build. hostTargets — main packages under the HOST build
	// context — drive the legacy --os/--arch product (unchanged behavior),
	// the cosmo fat APE (which embeds payloads for several native platforms,
	// so the host set is the sanest approximation), cosmo slot mapping, and
	// the host convenience symlinks. Explicit --targets entries additionally
	// get main-package discovery under their OWN GOOS/GOARCH context (see
	// resolvePlatformTargets), so a main guarded "//go:build js && wasm" is
	// built for js/wasm targets and never attempted for native ones. This
	// runs AFTER guard injection, which is safe because discovery skips the
	// guard file by name (gomod.MemLimitGuardFileName) — an unconstrained
	// guard in a host-only main dir cannot leak that dir into another
	// target's main set.
	hostTargets, err := build.ResolveBuildTargets(r)
	if err != nil {
		return err
	}

	platformTargets, anyMains, err := resolvePlatformTargets(platforms, hostTargets)
	if err != nil {
		return err
	}
	if !anyMains {
		return fmt.Errorf("no main packages found to build")
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}
	ensureBuildDirInGitignore()

	// Build job queue - one job per platform per main package
	var jobs []buildJob
	for _, p := range platforms {
		for _, target := range platformTargets[p] {
			outputName := build.BinaryName(target.OutputName, p.OS, p.Arch)
			if p.IsWasm() {
				// Publishable buildhost naming by default; the excluded
				// .wasm-suffixed shape under GO_TOOLCHAIN_WASM_PUBLISH=0.
				outputName = wasmArtifactName(target.OutputName, p)
			}
			job := buildJob{
				goos:       p.OS,
				goarch:     p.Arch,
				srcPath:    target.ImportPath,
				outputPath: filepath.Join(outputDir, outputName),
			}
			if p.NeedsForkToolchain() {
				job.forkGoroot = forkGoroot
				job.cacheNamespace = forkCacheNamespace
			}
			if p.IsCosmo() {
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
		logger.Info("⇒ Building %d binaries (%d targets)", len(jobs), len(platforms))
	} else {
		logger.Info("⇒ Building %d binaries (%d OS x %d arch)", len(jobs), len(matrixOS), len(matrixArch))
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
			logger.Info("  FAIL [%d/%d] %s/%s: %v %s", completed, len(jobs), result.job.goos, result.job.goarch, result.err, fmtDuration(result.duration))
			failed = append(failed, result)
		} else {
			logger.Info("  OK   [%d/%d] %s %s", completed, len(jobs), result.job.outputPath, fmtDuration(result.duration))
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
	if hasCosmo && len(slotPlatforms) > 0 {
		nativeBuilt := make(map[string]bool)
		for _, job := range jobs {
			if job.goos != cosmoOS {
				nativeBuilt[filepath.Base(job.outputPath)] = true
			}
		}
		copied, replacedFat, err := copyCosmoSlots(hostTargets, outputDir, slotPlatforms, nativeBuilt, os.Getenv("CI") != "")
		if err != nil {
			return err
		}
		builtFiles = append(builtFiles, copied...)
		for _, fat := range replacedFat {
			builtFiles = slices.DeleteFunc(builtFiles, func(p string) bool { return p == fat })
		}
	}

	// Wasm artifacts default to buildhost's publishable naming
	// (<name>_wasm_js / <name>_wasm_wasip1 — os=wasm with arch=js/wasip1,
	// wow-look-at-my/buildhost#166; pinned by
	// TestWasmArtifactNamesInBuildhostPublishSet). Publishing them requires a
	// buildhost with wasm artifact support: on an older server the upload
	// 400s (the same validation that rejected the pre-suffix `os=js` name,
	// field-confirmed on go-font-renderer run 29396682812) and one rejected
	// artifact aborts the whole publish — warn about the requirement and the
	// opt-out. Under GO_TOOLCHAIN_WASM_PUBLISH=0 the artifacts take the
	// excluded .wasm-suffixed shape instead, which never reaches the publish
	// upload set (the action only uploads files matching <binary>_{os}_{arch}
	// after stripping .exe) but still ships in build/, checksums.txt, and the
	// CI artifact.
	if hasWasm {
		if wasmPublishOptOut() {
			logger.Warn("⇒ Warning: %s=0 — wasm artifacts are excluded from buildhost publishing (.wasm-suffixed names stay outside the publish upload set); they remain in %s/ and checksums.txt for CI artifact uploads", wasmPublishEnv, outputDir)
			if !slices.ContainsFunc(platforms, func(p buildPlatform) bool { return !p.IsWasm() }) {
				logger.Warn("⇒ Warning: every target is wasm and %s=0, so a buildhost publish step will find no publishable artifacts and fail; disable autorelease for wasm-only builds with publishing opted out", wasmPublishEnv)
			}
		} else {
			logger.Warn("⇒ Warning: wasm artifacts publish to buildhost as os=wasm (arch=js/wasip1); this requires buildhost wasm artifact support (wow-look-at-my/buildhost#166) — on older servers the upload is rejected and aborts the whole publish; set %s=0 to keep wasm artifacts out of the publish set", wasmPublishEnv)
		}
	}

	// Consumers of a js/wasm artifact need the EXACT wasm_exec.js of the
	// toolchain that built it. Ship the fork's copy next to the artifact:
	// covered by checksums.txt and the CI artifact, but outside the buildhost
	// publish set — "wasm_exec.js" cannot match the publish action's
	// <binary>_{os}_{arch} filename pattern (pinned by
	// TestWasmArtifactNamesInBuildhostPublishSet). Best-effort: a fork
	// GOROOT without lib/wasm only warns.
	if forkGoroot != "" && slices.ContainsFunc(jobs, func(j buildJob) bool { return j.goos == "js" }) {
		if dst, err := copyWasmExecJS(forkGoroot, outputDir); err != nil {
			logger.Warn("⇒ Warning: could not copy wasm_exec.js from the fork toolchain: %v (browser/Node consumers must take lib/wasm/wasm_exec.js from the matching toolchain themselves)", err)
		} else {
			logger.Info("  COPY wasm_exec.js <- %s", filepath.Join(forkGoroot, "lib", "wasm"))
			builtFiles = append(builtFiles, dst)
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
		if err := createHostSymlinks(hostTargets, outputDir); err != nil {
			return err
		}
	}

	logger.Info("⇒ All %d binaries built successfully in %s/ %s", len(jobs), outputDir, fmtDuration(time.Since(buildStart)))

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
			logger.Info("  SKIP symlink for %s (host binary %s not found)", target.OutputName, hostBinary)
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
			logger.Info("  LINK %s -> %s", linkPath, hostBinary)
		}
	}
	return nil
}

// runBuild compiles a single binary. If onFirstOutput is non-nil, it is
// called when the compiler produces its first output (used for progress
// indicators on the default build path).
func runBuild(r runner.CommandRunner, job buildJob, onFirstOutput func()) error {
	// Last-chokepoint guard: a fork-toolchain job MUST carry a cache
	// namespace (see buildJob.cacheNamespace). Refusing to build here means a
	// future call site that forgets to fingerprint the toolchain fails loudly
	// instead of silently re-opening cross-toolchain cache poisoning.
	if job.forkGoroot != "" && job.cacheNamespace == "" {
		return fmt.Errorf("fork-toolchain build for %s/%s has no cache namespace; refusing to share the un-namespaced cache (see forkToolchainCacheNamespace)", job.goos, job.goarch)
	}
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
	if job.forkGoroot != "" {
		goCmd = filepath.Join(job.forkGoroot, "bin", "go")
	}
	cmd := runner.Cmd(goCmd, args...)
	switch {
	case job.forkGoroot != "" && job.goos == cosmoOS:
		// GOOS=cosmo fat-APE build via the gosmopolitan toolchain. GOARCH is
		// cleared: fat (amd64+arm64+windows payloads in one output) is the
		// fork's default and the job's pseudo-arch "fat" is a naming artifact,
		// not a GOARCH. GOCOSMOFAT is cleared too so an inherited =0 cannot
		// silently produce a thin binary that the slot copies would mislabel.
		// CGO_ENABLED=0 always: cosmopolitan has no cgo. The cache namespace
		// keys this build's cacheprog to THIS toolchain's content (its
		// cacheprog then skips the shared daemon and namespaces every key —
		// see cache.KeyNamespaceEnv), because the fork's constant version
		// stamp would otherwise collide its action IDs with every other fork
		// toolchain build's.
		cmd = cmd.WithEnv("GOOS", cosmoOS).
			WithEnv("GOARCH", "").
			WithEnv("GOCOSMOFAT", "").
			WithEnv("GOTOOLCHAIN", "local").
			WithEnv("GOROOT", job.forkGoroot).
			WithEnv("PATH", filepath.Join(job.forkGoroot, "bin")+string(os.PathListSeparator)+os.Getenv("PATH")).
			WithEnv("CGO_ENABLED", "0").
			WithEnv(cache.KeyNamespaceEnv, job.cacheNamespace)
	case job.forkGoroot != "":
		// Wasm build (js/wasm or wasip1/wasm) via the gosmopolitan toolchain.
		// The fork DEFAULTS to GOOS=cosmo, so GOOS and GOARCH are always
		// pinned explicitly. CGO_ENABLED=0 always: wasm has no cgo. The cache
		// namespace: same fork, same constant-version action-ID collisions,
		// same isolation (see the cosmo case above).
		cmd = cmd.WithEnv("GOOS", job.goos).
			WithEnv("GOARCH", job.goarch).
			WithEnv("GOTOOLCHAIN", "local").
			WithEnv("GOROOT", job.forkGoroot).
			WithEnv("PATH", filepath.Join(job.forkGoroot, "bin")+string(os.PathListSeparator)+os.Getenv("PATH")).
			WithEnv("CGO_ENABLED", "0").
			WithEnv(cache.KeyNamespaceEnv, job.cacheNamespace)
	default:
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
