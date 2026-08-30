package cmd

import (
	"sort"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// runBuildFunc is the seam the retry builds through, so a test can drive it without a compiler.
var runBuildFunc = runBuild

// retryCacheBrokenBuilds re-runs the builds the shared cache broke, with that
// cache tier switched off. A cache-served export entry the compiler cannot use
// is not a build failure: the packages it names were never really built, and it
// lands on whichever job asked for the entry, so the same source builds on the
// next run and reads as a flake. The test phase already recovers from this
// (exportdataretry.go); the build phase fails the whole run without this.
//
// Only the failures carrying that signature are retried, and only while the
// shared tier is in play. Anything else stays failed, untouched.
func retryCacheBrokenBuilds(r runner.CommandRunner, failed []buildResult, total int) (built []string, stillFailed []buildResult) {
	var broken []buildResult
	for _, result := range failed {
		if isCorruptExportData(result.err) {
			broken = append(broken, result)
		} else {
			stillFailed = append(stillFailed, result)
		}
	}
	if len(broken) == 0 {
		return nil, failed
	}
	if !disableSharedBuildCache() {
		for _, result := range broken {
			result.err = corruptExportDataError(result.err, false)
			stillFailed = append(stillFailed, result)
		}
		return nil, stillFailed
	}

	logger.Warn("⇒ Warning: %d/%d builds failed on a BUILD CACHE entry, not on your source: %s. Disabling the shared build cache (GOCACHEPROG) for the rest of this run and building them from source. Repeated occurrences mean the shared cache tier is serving damaged entries and needs inspecting.",
		len(broken), total, strings.Join(brokenPackages(broken), ", "))

	for _, result := range broken {
		logger.Info("  RETRY %s/%s without the shared build cache", result.job.goos, result.job.goarch)
		if err := runBuildFunc(r, result.job, nil); err != nil {
			if isCorruptExportData(err) {
				err = corruptExportDataError(err, true)
			}
			result.err = err
			stillFailed = append(stillFailed, result)
			continue
		}
		built = append(built, result.job.outputPath)
	}
	return built, stillFailed
}

// brokenPackages names every package the failed builds blamed, deduplicated
// across them, so the warning covers the whole set.
func brokenPackages(broken []buildResult) []string {
	seen := set.New[string]()
	for _, result := range broken {
		for _, pkg := range corruptExportPackages(result.err) {
			seen.Add(pkg)
		}
	}
	out := seen.Values()
	sort.Strings(out)
	return out
}
