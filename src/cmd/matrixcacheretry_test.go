package cmd

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// stubRunBuild replaces the compiler behind the retry for the duration of a test.
func stubRunBuild(fn func(buildJob) error) func() {
	prev := runBuildFunc
	runBuildFunc = func(_ runner.CommandRunner, job buildJob, _ func()) error { return fn(job) }
	return func() { runBuildFunc = prev }
}

// The real failure, copied from a consumer's CI job: the cosmo leg of the
// matrix asks for an export entry the cache promised and cannot open, and
// blames a package the change never touched.
const realMissingExportFileErr = `exit status 1
# github.com/wow-look-at-my/ffs.impl.ir/parser
parser/decl.go:4:2: could not import github.com/wow-look-at-my/ffs.impl.ir/ast (open ../../../.cache/go-toolchain/buildcache/e6/v1e66401b35b6c4a5c: no such file or directory)`

func TestIsCorruptExportDataMissingFile(t *testing.T) {
	assert.True(t, isCorruptExportData(errors.New(realMissingExportFileErr)))

	// The import frame is what makes it the cache's: any other missing file is not.
	assert.False(t, isCorruptExportData(errors.New("open /tmp/thing: no such file or directory")),
		"a missing file with no import around it is an ordinary error")
	assert.False(t, isCorruptExportData(errors.New(`could not import foo/bar (undefined: Baz)`)),
		"an import that failed for a source reason is a source error")
}

func TestCorruptExportPackagesMissingFile(t *testing.T) {
	assert.Equal(t, []string{"github.com/wow-look-at-my/ffs.impl.ir/ast"},
		corruptExportPackages(errors.New(realMissingExportFileErr)))
}

// Each failure names its own symptom, so a reader knows which of them they have.
func TestCorruptExportSymptom(t *testing.T) {
	assert.Contains(t, corruptExportDataError(errors.New(realMissingExportFileErr), false).Error(),
		"an export file that is no longer there")
	assert.Contains(t, corruptExportDataError(errors.New(realCorruptExportDataErr), false).Error(),
		"export data with no package name")
}

// A build the cache broke is retried with that tier off; anything else is left alone.
func TestRetryCacheBrokenBuildsSelectsAndRebuilds(t *testing.T) {
	t.Setenv("GOCACHEPROG", "some-cacheprog")

	var rebuilt []string
	restore := stubRunBuild(func(job buildJob) error {
		rebuilt = append(rebuilt, job.outputPath)
		require.Empty(t, os.Getenv("GOCACHEPROG"), "the retry must build with the shared tier off")
		return nil
	})
	defer restore()

	failed := []buildResult{
		{job: buildJob{goos: "cosmo", goarch: "fat", outputPath: "build/app"}, err: errors.New(realMissingExportFileErr)},
		{job: buildJob{goos: "linux", goarch: "amd64", outputPath: "build/app_linux_amd64"}, err: errors.New("undefined: Bar")},
	}

	built, stillFailed := retryCacheBrokenBuilds(nil, failed, 13)

	assert.Equal(t, []string{"build/app"}, built)
	assert.Equal(t, []string{"build/app"}, rebuilt)
	require.Len(t, stillFailed, 1, "a source error is not the cache's to retry")
	assert.Equal(t, "linux", stillFailed[0].job.goos)
}

// A repeat of the same shape is real, and says so.
func TestRetryCacheBrokenBuildsKeepsAFailureThatRepeats(t *testing.T) {
	t.Setenv("GOCACHEPROG", "some-cacheprog")

	restore := stubRunBuild(func(buildJob) error { return errors.New(realMissingExportFileErr) })
	defer restore()

	failed := []buildResult{{job: buildJob{goos: "cosmo", goarch: "fat"}, err: errors.New(realMissingExportFileErr)}}
	built, stillFailed := retryCacheBrokenBuilds(nil, failed, 13)

	assert.Empty(t, built)
	require.Len(t, stillFailed, 1)
	assert.Contains(t, stillFailed[0].err.Error(), "CORRUPT BUILD CACHE")
	assert.Contains(t, stillFailed[0].err.Error(), "go clean -cache")
}

// With no shared tier there is nothing to switch off, so the run must not pretend a retry would help.
func TestRetryCacheBrokenBuildsWithoutSharedCache(t *testing.T) {
	t.Setenv("GOCACHEPROG", "")

	restore := stubRunBuild(func(buildJob) error {
		require.Fail(t, "nothing to disable means nothing to retry")
		return nil
	})
	defer restore()

	failed := []buildResult{{job: buildJob{goos: "cosmo", goarch: "fat"}, err: errors.New(realMissingExportFileErr)}}
	built, stillFailed := retryCacheBrokenBuilds(nil, failed, 13)

	assert.Empty(t, built)
	require.Len(t, stillFailed, 1)
	assert.Contains(t, stillFailed[0].err.Error(), "the shared build cache (GOCACHEPROG) was not enabled")
}

func TestBrokenPackagesDeduplicatesAcrossBuilds(t *testing.T) {
	one := fmt.Sprintf("could not import a/b (open %s: no such file or directory)", "/c/v1x")
	two := fmt.Sprintf("could not import a/b (open %s: no such file or directory)\ncould not import c/d (open /c/v1y: no such file or directory)", "/c/v1x")

	assert.Equal(t, []string{"a/b", "c/d"}, brokenPackages([]buildResult{
		{err: errors.New(one)},
		{err: errors.New(two)},
	}))
}
