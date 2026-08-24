package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/build"
	"github.com/wow-look-at-my/go-toolchain/src/codeql"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

func runReleaseWithRunner(r runner.CommandRunner) (err error) {
	setupCGOEnvironment()
	// Same contract as staleoutputs.go: clear outputs up front, and again on failure.
	if err := clearBuildOutputs(r); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			discardBuildOutputs()
		}
	}()

	platforms, err := resolveMatrixPlatforms()
	if err != nil {
		return err
	}

	// Cosmo and wasm targets both build with the fork toolchain; fail fast before tests.
	hasCosmo := slices.ContainsFunc(platforms, buildPlatform.IsCosmo)
	hasWasm := slices.ContainsFunc(platforms, buildPlatform.IsWasm)
	var forkGoroot string
	var apePlatforms []buildPlatform
	if hasCosmo {
		if apePlatforms, err = parseCosmoPlatforms(cosmoPlatforms); err != nil {
			return err
		}
		if cgoEnabled {
			logger.Warn("⇒ Warning: --cgo has no effect on the cosmo target (cosmopolitan has no cgo; CGO_ENABLED=0 is forced)")
		}
	}
	if hasWasm && cgoEnabled {
		logger.Warn("⇒ Warning: --cgo has no effect on wasm targets (WebAssembly has no cgo; CGO_ENABLED=0 is forced)")
	}
	var forkCacheNamespace, apePlatformsEnv string
	if hasCosmo || hasWasm {
		if forkGoroot, err = ensureCosmoToolchainFunc(); err != nil {
			return err
		}
		// The fork's version stamp collides across builds, so cacheprog scopes
		// cache keys to this hash instead. Fail closed, not un-namespaced.
		if forkCacheNamespace, err = forkToolchainCacheNamespace(forkGoroot); err != nil {
			return fmt.Errorf("fingerprinting the fork toolchain for cache isolation: %w", err)
		}
		if hasCosmo {
			apePlatformsEnv = cosmoPlatformsEnvValue(forkGoroot, apePlatforms)
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

	// Runs before go-toolchain writes its own build artifacts, so those never fail this check.
	if err := checkDirtyInCI(); err != nil {
		return err
	}

	// Caps cross-compiled binaries' heap too via the GOMEMLIMIT guard; removed after the build.
	if err := injectMemLimitGuard(false); err != nil {
		return err
	}
	defer cleanupMemLimitGuards()

	// Drives the legacy build, cosmo APE, and symlinks; safe post-guard-injection since discovery skips the guard file by name.
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
				// Publishable buildhost naming by default; .wasm-suffixed under GO_TOOLCHAIN_WASM_PUBLISH=0.
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
				job.cosmoPlatforms = apePlatformsEnv
			}
			jobs = append(jobs, job)
		}
	}

	switch {
	case len(matrixTargets) == 0 && len(matrixOS) == 0 && len(matrixArch) == 0:
		logger.Info("⇒ Building %d fat APE(s) covering %s", len(jobs), platformList(apeCoverage(apePlatforms)))
	case len(matrixTargets) > 0:
		logger.Info("⇒ Building %d binaries (%d targets)", len(jobs), len(platforms))
	default:
		logger.Info("⇒ Building %d binaries (%d platforms from --os x --arch)", len(jobs), len(platforms))
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

	// One APE is one artifact whose identity is a platform SET, which the
	// <binary>_<os>_<arch> naming can't spell. The manifest records it as one
	// upload/row/link and excludes it from that filename scan, letting it
	// publish under the plain name.
	if hasCosmo {
		entries, err := apeManifestEntries(hostTargets, outputDir, apeCoverage(apePlatforms))
		if err != nil {
			return err
		}
		if _, err := writeBuildhostManifest(outputDir, entries); err != nil {
			return err
		}
		logger.Info("  WRITE %s (%d APE artifact(s), platforms %s)", buildhostManifestName, len(entries), platformList(apeCoverage(apePlatforms)))
	}

	// Wasm artifacts default to buildhost's publishable naming
	// (<name>_wasm_js / <name>_wasm_wasip1), which needs a buildhost with
	// wasm artifact support -- an older server rejects the upload and aborts
	// the whole publish, so warn about the requirement and the opt-out.
	// GO_TOOLCHAIN_WASM_PUBLISH=0 switches to the excluded .wasm-suffixed
	// shape, which the publish upload set never matches (it only takes
	// <binary>_{os}_{arch} after stripping .exe) but still ships in build/,
	// checksums.txt, and the CI artifact.
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

	// Host/bare symlinks; skipped in CI, since upload-artifact dereferences symlinks into full duplicate copies.
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

	// Host-runnable copies feed dats; a cosmo artifact self-assimilates, so copies are staged, never run in place.
	hostArtifacts := make([]datsArtifact, 0, len(hostTargets))
	for _, t := range hostTargets {
		hostArtifacts = append(hostArtifacts, datsArtifact{
			sourcePath: hostRunnableArtifact(t, outputDir),
			name:       datsArtifactName(t.OutputName, hostos.GOOS()),
		})
	}
	if err := runDatsPhase(false, hostArtifacts); err != nil {
		return err
	}

	return nil
}
