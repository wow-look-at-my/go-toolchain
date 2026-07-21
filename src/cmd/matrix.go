package cmd

import (
	"context"
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
