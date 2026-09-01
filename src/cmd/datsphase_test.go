package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	dats "github.com/wow-look-at-my/dats"
	datsrunner "github.com/wow-look-at-my/dats/runner"
)

// forceDatsProbe pins the backend answer, so a sandbox assertion reads the
// code rather than the host it runs on.
func forceDatsProbe(t *testing.T, err error) {
	t.Helper()
	previous := datsSandboxProbe
	datsSandboxProbe = func() error { return err }
	t.Cleanup(func() { datsSandboxProbe = previous })
}

// The suites are what a host is covered BY, so a host that can never sandbox
// must still run them. A host merely missing bubblewrap must not: an install
// fixes that, and dropping isolation there is how it stays missing.
func TestDatsSandbox(t *testing.T) {
	t.Run("a host with a backend runs sandboxed", func(t *testing.T) {
		forceDatsProbe(t, nil)
		assert.Equal(t, dats.Sandbox{}, datsSandbox(), "the zero Sandbox is auto")
	})

	t.Run("a host that can never sandbox runs on the host", func(t *testing.T) {
		forceDatsProbe(t, fmt.Errorf("%w: no usable sandbox backend", datsrunner.ErrNoBackendOnHost))
		assert.Equal(t, datsrunner.SandboxNone, datsSandbox().Mode)
	})

	t.Run("a fixable gap keeps asking for a sandbox", func(t *testing.T) {
		forceDatsProbe(t, fmt.Errorf("no usable sandbox backend: bwrap: not found in $PATH"))
		assert.Equal(t, dats.Sandbox{}, datsSandbox(), "installing bubblewrap fixes this, so the run must still fail")
	})
}

func TestHasDatsSuites(t *testing.T) {
	write := func(t *testing.T, dir, rel string) {
		t.Helper()
		path := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("tests: []\n"), 0o644))
	}

	t.Run("no dats dir", func(t *testing.T) {
		assert.False(t, hasDatsSuites(t.TempDir()))
	})

	t.Run("dats is a file, not a dir", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "dats"), nil, 0o644))
		assert.False(t, hasDatsSuites(dir))
	})

	t.Run("empty dats dir", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "dats"), 0o755))
		assert.False(t, hasDatsSuites(dir))
	})

	t.Run("non-dats files only", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "dats/README.md")
		write(t, dir, "dats/cli.dats.snapshots/001-x.stdout.golden")
		assert.False(t, hasDatsSuites(dir))
	})

	t.Run("suite at top level", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "dats/cli.dats")
		assert.True(t, hasDatsSuites(dir))
	})

	t.Run("suite in subdirectory", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "dats/sub/more.dats")
		assert.True(t, hasDatsSuites(dir))
	})

	t.Run("hidden suite file skipped", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "dats/.hidden.dats")
		assert.False(t, hasDatsSuites(dir))
	})

	t.Run("hidden dir skipped", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "dats/.git/x.dats")
		assert.False(t, hasDatsSuites(dir))
	})

	t.Run("nested module still counts", func(t *testing.T) {
		// dats' own discovery does not skip nested Go modules, so the gate must not either.
		dir := t.TempDir()
		write(t, dir, "dats/fixturemod/go.mod")
		write(t, dir, "dats/fixturemod/x.dats")
		assert.True(t, hasDatsSuites(dir))
	})
}

func TestDatsArtifactName(t *testing.T) {
	assert.Equal(t, "mytool", datsArtifactName("mytool", "linux"))
	assert.Equal(t, "mytool", datsArtifactName("mytool", "darwin"))
	assert.Equal(t, "mytool.exe", datsArtifactName("mytool", "windows"))
}

func TestStageDatsArtifacts(t *testing.T) {
	src := t.TempDir()
	real := filepath.Join(src, "mytool_linux_amd64")
	require.NoError(t, os.WriteFile(real, []byte("binary bytes"), 0o644))

	dir, err := stageDatsArtifacts([]datsArtifact{
		{sourcePath: real, name: "mytool"},
		{sourcePath: filepath.Join(src, "missing_darwin_arm64"), name: "missing"},
	})
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	// The present artifact is copied under its bare name, executable.
	staged := filepath.Join(dir, "mytool")
	data, err := os.ReadFile(staged)
	require.NoError(t, err)
	assert.Equal(t, []byte("binary bytes"), data)
	assertExecutable(t, staged, "staged artifact must be executable")

	// The missing artifact is skipped without failing the staging.
	assert.NoFileExists(t, filepath.Join(dir, "missing"))
}

// datsCall records what the phase asked the dats library to do.
type datsCall struct {
	opts dats.Options
}

// swapDatsRun replaces the dats library seam for a single test, returning the
// recorded calls.
func swapDatsRun(t *testing.T, res *dats.Result, err error) *[]datsCall {
	t.Helper()
	calls := &[]datsCall{}
	old := datsRunFunc
	datsRunFunc = func(_ context.Context, opts dats.Options) (*dats.Result, error) {
		*calls = append(*calls, datsCall{opts: opts})
		return res, err
	}
	t.Cleanup(func() { datsRunFunc = old })
	return calls
}

// okResult is a green run of n tests.
func okResult(n int) *dats.Result {
	return &dats.Result{Passed: n, Files: []*datsrunner.FileResult{{Passed: n}}}
}

// chdirWithSuite creates a temp module dir containing dats/cli.dats and
// chdirs into it for the duration of the test.
func chdirWithSuite(t *testing.T) (dir string) {
	t.Helper()
	dir = t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "dats"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dats", "cli.dats"), []byte("tests: []\n"), 0o644))
	t.Chdir(dir)
	return dir
}

func TestRunDatsPhaseNoSuitesIsNoOp(t *testing.T) {
	t.Chdir(t.TempDir())
	calls := swapDatsRun(t, okResult(0), nil)

	require.NoError(t, runDatsPhase(false, nil))
	assert.Empty(t, *calls, "no suites means dats is never invoked at all")
}

func TestRunDatsPhaseRunsSuites(t *testing.T) {
	dir := chdirWithSuite(t)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "build"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "build", "mytool"), []byte("bin"), 0o755))

	// Inspect the staged handoff dir DURING the call -- it is removed when the phase returns.
	var stagedBinary string
	old := datsRunFunc
	datsRunFunc = func(_ context.Context, opts dats.Options) (*dats.Result, error) {
		buildDir := datsEnvValue(t, opts.Env, datsBuildDirEnv)
		stagedBinary = filepath.Join(buildDir, "mytool")
		_, err := os.Stat(stagedBinary)
		assert.NoError(t, err)
		return okResult(1), nil
	}
	t.Cleanup(func() { datsRunFunc = old })

	artifacts := []datsArtifact{{sourcePath: filepath.Join(dir, "build", "mytool"), name: "mytool"}}
	require.NoError(t, runDatsPhase(false, artifacts))
	require.NotEmpty(t, stagedBinary)
	assert.NoFileExists(t, stagedBinary, "handoff dir must be cleaned up after the run")
}

func TestRunDatsPhaseOptions(t *testing.T) {
	dir := chdirWithSuite(t)
	calls := swapDatsRun(t, okResult(1), nil)
	forceDatsProbe(t, nil) // an NT host has none, and would hand dats SandboxNone
	require.NoError(t, runDatsPhase(false, nil))

	require.Len(t, *calls, 1)
	opts := (*calls)[0].opts
	assert.Equal(t, []string{datsSuiteDir}, opts.Paths)

	// Serial on purpose: a deterministic report, no concurrent APE self-assimilation.
	assert.Zero(t, opts.Jobs)

	// The phase hands dats what datsSandbox decided: auto, for the host pinned above.
	assert.Equal(t, dats.Sandbox{}, opts.Sandbox)

	// The handoff dir must be absolute and inside the module root: only the cwd is visible in the sandbox.
	buildDir := datsEnvValue(t, opts.Env, datsBuildDirEnv)
	assert.True(t, filepath.IsAbs(buildDir), "handoff dir must be absolute, got %q", buildDir)
	rel, relErr := filepath.Rel(dir, buildDir)
	require.NoError(t, relErr)
	assert.False(t, strings.HasPrefix(rel, ".."),
		"handoff dir %q must live inside the module root %q, or the sandbox cannot see it", buildDir, dir)
	assert.Equal(t, filepath.Join(outputDir, datsStageDir), rel)

	// The shared cache must be cleared for suite commands.
	assert.Equal(t, "", datsEnvValue(t, opts.Env, "GO_BUILDCACHE_CONFIG"))
}

// datsEnvValue returns the value of key in a dats Options.Env list, failing
// the test when the entry is absent.
func datsEnvValue(t *testing.T, env []string, key string) string {
	t.Helper()
	for _, e := range env {
		if name, value, ok := strings.Cut(e, "="); ok && name == key {
			return value
		}
	}
	t.Fatalf("dats must receive %s (env: %v)", key, env)
	return ""
}

func TestRunDatsPhaseFailingTestsFailBuild(t *testing.T) {
	chdirWithSuite(t)
	// A red suite is a Result, not an error, from the library -- the phase is
	// what turns it into a failed build.
	swapDatsRun(t, &dats.Result{
		Passed: 2,
		Failed: 1,
		Files:  []*datsrunner.FileResult{{Passed: 2, Failed: 1}},
	}, nil)

	err := runDatsPhase(false, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dats suites failed")
	assert.Contains(t, err.Error(), "1 of 3 tests failed")
}

func TestRunDatsPhaseTeardownFailureFailsBuild(t *testing.T) {
	chdirWithSuite(t)
	// Ok() is not an empty Failed count: a file whose teardown failed fails the run even
	// with every test green.
	swapDatsRun(t, &dats.Result{
		Passed: 1,
		Files: []*datsrunner.FileResult{{
			Passed:           1,
			TeardownFailures: []datsrunner.CommandFailure{{Command: "cleanup", Detail: "exit code 3"}},
		}},
	}, nil)

	require.Error(t, runDatsPhase(false, nil))
}

func TestRunDatsPhaseHardErrorFailsBuild(t *testing.T) {
	chdirWithSuite(t)
	swapDatsRun(t, nil, fmt.Errorf("no usable sandbox backend"))

	err := runDatsPhase(false, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dats suites failed")
	assert.Contains(t, err.Error(), "no usable sandbox backend")
}

func TestRunDatsPhaseQuietRoutesReportToStderr(t *testing.T) {
	chdirWithSuite(t)
	calls := swapDatsRun(t, okResult(1), nil)

	require.NoError(t, runDatsPhase(true, nil))
	require.Len(t, *calls, 1)
	// --json mode: stdout carries the JSON payload, so the report goes to stderr, unwrapped.
	assert.Equal(t, rawStderr, (*calls)[0].opts.Output)
}

func TestRunDatsPhaseOutputTerminatesTheStepLine(t *testing.T) {
	chdirWithSuite(t)
	var noted int
	w := &noteFirstWrite{w: &bytes.Buffer{}, note: func() { noted++ }}

	_, _ = w.Write([]byte("first"))
	_, _ = w.Write([]byte("second"))
	_, _ = w.Write(nil)
	assert.Equal(t, 1, noted, "the step line is terminated exactly once, on the first real byte")
}

// A repo with suites but no go.mod runs them. Before this, go-toolchain exited
// on "no go.mod found" before doing anything, so shell and TypeScript repos
// with a CLI worth testing had to fetch a standalone dats and wire their own CI
// step -- duplicating what this binary already links in, at a version free to
// drift from it.
func TestRunDatsOnlyRunsSuitesWithoutAModule(t *testing.T) {
	dir := chdirWithSuite(t)
	calls := swapDatsRun(t, okResult(2), nil)

	require.NoError(t, runDatsOnly())

	require.Len(t, *calls, 1, "the suites are the whole run")
	assert.Equal(t, []string{datsSuiteDir}, (*calls)[0].opts.Paths)
	assert.NoFileExists(t, filepath.Join(dir, "go.mod"), "no module was needed")
}

// Staging has to live under the working directory for the sandbox to see it,
// but a non-Go repo does not gitignore build/ and never asked for that directory.
func TestRunDatsOnlyLeavesNoStrayBuildDir(t *testing.T) {
	dir := chdirWithSuite(t)
	swapDatsRun(t, okResult(1), nil)

	require.NoError(t, runDatsOnly())
	assert.NoDirExists(t, filepath.Join(dir, outputDir),
		"an empty build/ created purely for staging must be taken back out")
}

// ...but a build/ that was already there is the repo's, not ours to delete.
func TestRunDatsOnlyKeepsAPreexistingBuildDir(t *testing.T) {
	dir := chdirWithSuite(t)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, outputDir), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, outputDir, "keep.txt"), []byte("mine"), 0o644))
	swapDatsRun(t, okResult(1), nil)

	require.NoError(t, runDatsOnly())
	assert.FileExists(t, filepath.Join(dir, outputDir, "keep.txt"))
}

// A failing suite still fails the run -- the point of running them at all.
func TestRunDatsOnlyPropagatesFailure(t *testing.T) {
	chdirWithSuite(t)
	swapDatsRun(t, &dats.Result{Passed: 1, Failed: 1,
		Files: []*datsrunner.FileResult{{Passed: 1, Failed: 1}}}, nil)

	require.Error(t, runDatsOnly())
}
