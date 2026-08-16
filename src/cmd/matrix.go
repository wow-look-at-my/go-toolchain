package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"github.com/wow-look-at-my/go-toolchain/src/summary"
	gotrace "github.com/wow-look-at-my/go-toolchain/src/trace"
)

var (
	matrixOS        []string
	matrixArch      []string
	matrixTargets   []string
	cosmoPlatforms  []string
	releaseParallel int
)

// DefaultOS / DefaultArch fill in the half of the cartesian product the caller
// left out. They are NOT the flags' defaults: an empty --os and --arch is what
// selects the single-APE default (see resolveMatrixPlatforms).
var (
	DefaultOS   = []string{"linux", "darwin", "windows"}
	DefaultArch = []string{"amd64", "arm64"}
)

func init() {
	matrixCmd := &cobra.Command{
		Use:   "matrix",
		Short: "Cross-compile for multiple platforms",
		Long: `Builds ONE fat Actually Portable Executable covering several platforms, or
binaries for multiple GOOS/GOARCH combinations in parallel.

By default the matrix builds a single cosmo APE (artifact <name>_cosmo_fat)
covering --cosmo-platforms: linux/amd64, darwin/arm64 and windows/amd64. One
file runs on all three; there is no per-platform copy.

--os and --arch bring back the cartesian product of native per-platform
binaries; naming either one selects it. --targets replaces both with an exact
list, each entry an os/arch pair (e.g. darwin/amd64) or the special value
"cosmo" for the fat APE.

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
	cmd.Flags().StringSliceVar(&matrixOS, "os", nil, "Target operating systems; naming either --os or --arch switches from the single-APE default to per-platform binaries (default linux,darwin,windows when only --arch is given)")
	cmd.Flags().StringSliceVar(&matrixArch, "arch", nil, "Target architectures; naming either --os or --arch switches from the single-APE default to per-platform binaries (default amd64,arm64 when only --os is given)")
	cmd.Flags().StringSliceVar(&matrixTargets, "targets", nil, `Exact build targets as os/arch pairs (incl. wasm/js and wasm/wasip1, built with the gosmopolitan toolchain) plus the special value "cosmo" (a gosmopolitan fat APE); replaces the --os x --arch product`)
	cmd.Flags().StringSliceVar(&cosmoPlatforms, "cosmo-platforms", DefaultCosmoPlatforms, `Host platforms the cosmo fat APE must cover, as os/arch pairs ("all" covers every platform the fork can emit)`)
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
	// cosmoPlatforms is the GOCOSMOPLATFORMS value for a fat-APE job: the
	// host platforms the APE must cover. Empty leaves the variable unset,
	// which is the fork's everything-default.
	cosmoPlatforms string
}

type buildResult struct {
	job      buildJob
	err      error
	duration time.Duration
}

// runMatrixModules cross-compiles every module in the tree, the way the
// default pipeline gates every module. A repository whose root carries no
// go.mod is a tree of modules, not a broken one -- before this, matrix tidied
// the root, found nothing, and died on "no go.mod found" while the same tree
// built fine under a bare go-toolchain.
func runMatrixModules(r runner.CommandRunner) error {
	modules := findGoModules()
	if len(modules) == 0 {
		// Suites without a module are the whole run, as in the default
		// pipeline: the CLI a suite drives does not have to be Go.
		if hasDatsSuites(".") {
			return runDatsOnly()
		}
		return fmt.Errorf("no go.mod and no dats/ suites found — initialize a module with: go mod init <module-path>")
	}

	startDir, err := os.Getwd()
	if err != nil {
		return err
	}
	defer os.Chdir(startDir)

	libraryModulesAllowed = len(modules) > 1
	matrixBuiltBinaries = 0

	for i, modDir := range modules {
		if len(modules) > 1 {
			if i > 0 {
				logger.Info("")
			}
			logger.Info("⇒ Module: %s", modDir)
		}
		if modDir != "." {
			if err := os.Chdir(filepath.Join(startDir, modDir)); err != nil {
				return fmt.Errorf("failed to enter %s: %w", modDir, err)
			}
		}
		if err := runReleaseWithRunner(r); err != nil {
			return err
		}
	}

	// Every module was a library. The command exists to produce binaries, so
	// a run that produced none is a failure, not a quiet success.
	if matrixBuiltBinaries == 0 {
		return fmt.Errorf("no main packages found to build in any of the %d modules", len(modules))
	}
	return nil
}

func runRelease(cmd *cobra.Command, args []string) error {
	InitTimeline()
	// Collect per-action build profiles for every cross-compile target. The
	// matrix path has no Chrome trace, but the deferred capture still parses
	// and stashes the graphs so printCacheStats can emit the final report.
	initBuildProfile()
	defer captureProfileTrace()
	r := runner.New()
	if err := runMatrixModules(r); err != nil {
		return err
	}

	if err := maybeSubmitDeps(); err != nil {
		return err
	}

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
