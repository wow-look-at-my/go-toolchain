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
	matrixTargets   []string
	cosmoPlatforms  []string
	releaseParallel int
)

func init() {
	matrixCmd := &cobra.Command{
		Use:   "matrix",
		Short: "Build the release APE (and optional wasm targets)",
		Long: `Builds ONE fat Actually Portable Executable: the org's only native release
output (see docs/MATRIX.md).

By default the build produces a single cosmo APE (artifact <name>) covering
--cosmo-platforms: linux/amd64, darwin/arm64 and windows/amd64. One file runs
on all three.

The WebAssembly targets wasm/js (browser/Node.js) and wasm/wasip1 (WASI) are
built with the gosmopolitan fork toolchain (it carries the org's wasm runtime
fixes) and opted into with --targets, e.g. --targets cosmo,wasm/js to build
both, or --targets wasm/js,wasm/wasip1 for wasm alone. The GOOS-order
spellings js/wasm and wasip1/wasm are accepted as compatibility aliases for
the same targets. Their artifacts use buildhost's publishable wasm naming
(<name>_wasm_js, <name>_wasm_wasip1 — os=wasm with arch=js/wasip1, no file
extension); publishing them requires a buildhost with wasm artifact support.
Set GO_TOOLCHAIN_WASM_PUBLISH=0 to use the excluded <name>_<goos>_wasm.wasm
naming instead, which never reaches the buildhost publish upload set.`,
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
	cmd.Flags().StringSliceVar(&matrixTargets, "targets", nil, `Wasm targets to add (wasm/js, wasm/wasip1, built with the gosmopolitan toolchain) plus the special value "cosmo" (a gosmopolitan fat APE); default is "cosmo" alone`)
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
