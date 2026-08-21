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
	"github.com/wow-look-at-my/go-toolchain/src/cosmocompat"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// libraryModulesAllowed lets a module with no main package pass through the
// build phase with an empty job list instead of failing the run. runRelease
// sets it while it walks a tree of modules: a library beside the module that
// ships the binary is the ordinary shape there, and its tests and dats suites
// still have to run. A single-module repo leaves it false, so "no main
// packages found to build" still fails that run.
//
// matrixBuiltBinaries counts what every module built, so a whole run that
// produced no binary at all still fails.
var (
	libraryModulesAllowed bool
	matrixBuiltBinaries   int
)

func runReleaseWithRunner(r runner.CommandRunner) (err error) {
	setupCGOEnvironment()
	// Same contract as the default pipeline (see staleoutputs.go): delete the
	// artifacts up front so nothing runnable survives a failure, and delete
	// them again if the run goes red after the builds wrote them.
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

	// Resolve the gosmopolitan-fork prerequisites up front so a missing
	// toolchain or a bad --cosmo-platforms value fails fast, before the test
	// phase. Both the cosmo fat APE and the wasm targets build with the fork.
	hasCosmo := slices.ContainsFunc(platforms, buildPlatform.IsCosmo)
	hasWasm := slices.ContainsFunc(platforms, buildPlatform.IsWasm)
	var forkGoroot string
	var apePlatforms []buildPlatform
	var cosmoGoWork string
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
		if hasCosmo {
			apePlatformsEnv = cosmoPlatformsEnvValue(forkGoroot, apePlatforms)
		}
	}

	// Some third-party modules ship no cosmo port at all (modernc.org/libc,
	// golang.org/x/sys, modernc.org/sqlite as of this writing). Prepare is a
	// silent no-op (empty path, nil error) for a module graph that depends on
	// none of them; only reached once the fork toolchain itself is confirmed
	// available, so a genuinely missing toolchain still fails with THAT
	// error, not a confusing go.mod-parsing error from this step. See
	// docs/COSMOCOMPAT.md.
	if hasCosmo {
		var cosmoCleanup func()
		if cosmoGoWork, cosmoCleanup, err = cosmocompat.Prepare("."); err != nil {
			return fmt.Errorf("preparing cosmo build compatibility patches: %w", err)
		}
		defer cosmoCleanup()
		if cosmoGoWork != "" {
			logger.Info("⇒ Cosmo compat: patched third-party module(s) with no cosmo port (see docs/COSMOCOMPAT.md)")
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
	// so the host set is the sanest approximation), the publish manifest, and
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
	if !anyMains && !libraryModulesAllowed {
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
				job.cosmoPlatforms = apePlatformsEnv
				job.goWork = cosmoGoWork
			}
			jobs = append(jobs, job)
		}
	}

	switch {
	case len(jobs) == 0:
		logger.Info("⇒ No main package here, so there is nothing to cross-compile")
	case len(matrixTargets) == 0 && len(matrixOS) == 0 && len(matrixArch) == 0:
		logger.Info("⇒ Building %d fat APE(s) covering %s", len(jobs), platformList(apeCoverage(apePlatforms)))
	case len(matrixTargets) > 0:
		logger.Info("⇒ Building %d binaries (%d targets)", len(jobs), len(platforms))
	default:
		logger.Info("⇒ Building %d binaries (%d platforms from --os x --arch)", len(jobs), len(platforms))
	}
	matrixBuiltBinaries += len(jobs)
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

	// One APE is one artifact. Its identity is a platform SET, which the
	// <binary>_<os>_<arch> filename grammar cannot spell, so it travels in a
	// manifest buildhost-publish reads from the published directory: one
	// upload, one artifact row, one download link for every platform it
	// covers. Listing the file there also takes it out of that filename scan,
	// which is what lets the APE publish under the plain name.
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
	// A module that built nothing has no host binary to point at, and saying
	// so once above is enough.
	if os.Getenv("CI") == "" && len(jobs) > 0 {
		if err := createHostSymlinks(hostTargets, outputDir); err != nil {
			return err
		}
	}

	if len(jobs) > 0 {
		logger.Info("⇒ All %d binaries built successfully in %s/ %s", len(jobs), outputDir, fmtDuration(time.Since(buildStart)))
	}

	// Run benchmarks after successful build
	if !noBenchmark {
		if _, err := runBenchmarkInBuild(r); err != nil {
			return err
		}
	}

	// Run the module's dats suites (if any) against host-runnable copies of
	// the matrix artifacts. The host-named artifact may BE the cosmo fat APE
	// (the default cosmo build), which self-assimilates on first exec — the phase
	// stages throwaway copies, never executing anything in build/ in place.
	// A cross-only build with no host artifact still runs the suites (the
	// missing copy is skipped; tests that need it fail honestly).
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
