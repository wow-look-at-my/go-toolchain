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
	cosmoPlatforms  []string
	releaseParallel int
)

// DefaultOS / DefaultArch fill the product's other half; not flag defaults
// (empty --os/--arch selects the single-APE default).
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

By default the matrix builds a single cosmo APE (artifact <name>)
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
	// forkGoroot is the gosmopolitan GOROOT for fat-APE/wasm jobs; empty for normal jobs (go on PATH).
	forkGoroot string
	// cacheNamespace scopes cache keys per fork toolchain; required with forkGoroot or builds share keys and poison the cache.
	cacheNamespace string
	// cosmoPlatforms is GOCOSMOPLATFORMS for a fat-APE job; empty leaves it unset (the fork's everything-default).
	cosmoPlatforms string
}

type buildResult struct {
	job      buildJob
	err      error
	duration time.Duration
}

func runRelease(cmd *cobra.Command, args []string) error {
	InitTimeline()
	// Collects per-action build profiles; no Chrome trace here, but the deferred capture still parses graphs for printCacheStats.
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

	// Fails the run once every phase has printed if warnings exceed maxWarnings (same gate as the default pipeline).
	return checkWarningsGate()
}
