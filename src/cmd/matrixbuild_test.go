package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/build"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// hostLinkName spells a link the way this host runs it: NT needs the .exe suffix.
func hostLinkName(name string) string {
	if hostos.GOOS() == "windows" {
		return name + ".exe"
	}
	return name
}

// wasmJob is a build job runBuild accepts, for tests about everything other
// than which targets are allowed (that is TestRunBuildRefusesAnythingButThePortableTargets).
func wasmJob(t *testing.T, outputPath string) buildJob {
	t.Helper()
	return buildJob{
		goos:           "wasip1",
		goarch:         wasmArch,
		srcPath:        ".",
		outputPath:     outputPath,
		forkGoroot:     filepath.Join(t.TempDir(), "fork-goroot"),
		cacheNamespace: "deadbeef00c0ffee",
	}
}

// cosmoJob is the fat-APE counterpart of wasmJob: the shipped target, whose
// bytes every host has to agree on.
func cosmoJob(t *testing.T, outputPath string) buildJob {
	t.Helper()
	job := wasmJob(t, outputPath)
	job.goos = cosmoOS
	job.goarch = ""
	return job
}

// tmpOut names a build output in a fresh directory.
func tmpOut(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "out")
}

func TestRunBuildCapturesStderr(t *testing.T) {
	t.Parallel()
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if isGoBuild(cfg) {
			return runner.MockProcessWithStderr(nil, []byte("./main.go:5:3: undefined: foo\n"), fmt.Errorf("exit status 1")), nil
		}
		return nil, nil
	}

	err := runBuild(mock, wasmJob(t, tmpOut(t)), nil)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "exit status 1")
	assert.Contains(t, err.Error(), "undefined: foo")
}

func TestRunBuildNoStderrOnSuccess(t *testing.T) {
	t.Parallel()
	mock := runner.NewMock()
	// The mocked compiler obeys a real compiler's contract: a successful go build
	// materializes its -o target.
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		writeMockBuildOutput(cfg, "bin")
		return runner.MockProcess(nil, nil), nil
	}
	job := wasmJob(t, tmpOut(t))

	err := runBuild(mock, job, nil)
	assert.Nil(t, err)
	assert.FileExists(t, job.outputPath, "the commit moved the build onto the target name")
}

// The compiler is a file on disk, and on a windows host that file is go.exe.
// runBuild is where everything compiles, so a path spelled without the suffix
// fails to exec every build on that host.
func TestRunBuildExecsGoExeOnWindowsHost(t *testing.T) {
	oldHost := cosmoHostPlatformFunc
	cosmoHostPlatformFunc = func() (string, string) { return "windows", "amd64" }
	t.Cleanup(func() { cosmoHostPlatformFunc = oldHost })

	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		writeMockBuildOutput(cfg, "bin")
		return runner.MockProcess(nil, nil), nil
	}
	job := wasmJob(t, tmpOut(t))

	require.NoError(t, runBuild(mock, job, nil))
	calls := mock.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, filepath.Join(job.forkGoroot, "bin", "go.exe"), calls[0].Name)
}

// The APE claims to run on every host, and that claim is honest only if every
// host builds the same bytes. What differs between runners is where the source
// is checked out and which fork build compiled it. Both reach the output
// through the build-ID notes, and each flag closes its own channel, so a build
// missing either still leaves the hosts disagreeing.
func TestRunBuildIsReproducibleAcrossHosts(t *testing.T) {
	t.Parallel()
	for _, job := range []buildJob{wasmJob(t, tmpOut(t)), cosmoJob(t, tmpOut(t))} {
		t.Run(job.goos, func(t *testing.T) {
			mock := runner.NewMock()
			mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
				writeMockBuildOutput(cfg, "bin")
				return runner.MockProcess(nil, nil), nil
			}

			require.NoError(t, runBuild(mock, job, nil))
			calls := mock.Calls()
			require.Len(t, calls, 1)
			assert.Contains(t, calls[0].Args, "-trimpath",
				"every build goes through runBuild, so a target missing -trimpath ships a binary carrying the path it was built at")
			assert.Contains(t, calls[0].Args, reproducibleLDFlags,
				"without an emptied build ID the note records which fork build compiled this, and no two hosts share one")
		})
	}
}

// An explicit ldflags survives, and the reproducibility flag still wins:
// dropping it silently would give up cross-host identity without saying so.
func TestRunBuildKeepsCallerLDFlagsAndStillEmptiesTheBuildID(t *testing.T) {
	t.Parallel()
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		writeMockBuildOutput(cfg, "bin")
		return runner.MockProcess(nil, nil), nil
	}
	job := cosmoJob(t, tmpOut(t))
	job.ldflags = "-X main.version=test"

	require.NoError(t, runBuild(mock, job, nil))
	calls := mock.Calls()
	require.Len(t, calls, 1)

	var got string
	for i, arg := range calls[0].Args {
		if arg == "-ldflags" && i+1 < len(calls[0].Args) {
			got = calls[0].Args[i+1]
		}
	}
	assert.Equal(t, "-X main.version=test "+reproducibleLDFlags, got,
		"the caller's flags survive and the build-ID flag comes last, where the linker reads it as authoritative")
}

func TestRunBuild(t *testing.T) {
	t.Parallel()
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		writeMockBuildOutput(cfg, "bin")
		return runner.MockProcess(nil, nil), nil
	}
	job := wasmJob(t, tmpOut(t))

	err := runBuild(mock, job, nil)
	assert.Nil(t, err)

	// Verify command was called
	calls := mock.Calls()
	assert.Equal(t, 1, len(calls))

	// Verify env vars were set
	cfg := calls[0]
	goos, _ := cfg.Env.Get("GOOS")
	goarch, _ := cfg.Env.Get("GOARCH")
	goroot, _ := cfg.Env.Get("GOROOT")
	assert.Equal(t, "wasip1", goos)
	assert.Equal(t, wasmArch, goarch)
	assert.Equal(t, job.forkGoroot, goroot)

	// -o is the .tmp- spelling, never the target file itself.
	hasOutput := false
	for i, arg := range cfg.Args {
		if arg == "-o" && i+1 < len(cfg.Args) {
			hasOutput = cfg.Args[i+1] == build.TempOutputPath(job.outputPath)
		}
	}
	assert.True(t, hasOutput)
}

// --cgo cannot turn cgo on: the APE and wasm both lack it, so CGO_ENABLED is
// assigned off either way rather than left to the flag or the environment.
func TestRunBuildForcesCGOOffEvenWithTheFlag(t *testing.T) {
	for _, flag := range []bool{false, true} {
		t.Run(fmt.Sprintf("cgoEnabled=%v", flag), func(t *testing.T) {
			oldCgo := cgoEnabled
			cgoEnabled = flag
			defer func() { cgoEnabled = oldCgo }()

			mock := runner.NewMock()
			mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
				writeMockBuildOutput(cfg, "bin")
				return runner.MockProcess(nil, nil), nil
			}
			require.NoError(t, runBuild(mock, wasmJob(t, tmpOut(t)), nil))

			calls := mock.Calls()
			require.Len(t, calls, 1)
			cgo, ok := calls[0].Env.Get("CGO_ENABLED")
			assert.True(t, ok, "CGO_ENABLED must be assigned, not inherited")
			assert.Equal(t, "0", cgo)
		})
	}
}

// The APE already occupies the bare name, so only the _host convenience link
// is created. Overwriting the bare name would delete the artifact itself.
func TestCreateHostSymlinks(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	targets := []build.Target{
		{ImportPath: "./cmd/mytool", OutputName: "mytool"},
	}

	ape := filepath.Join(tmpDir, "mytool")
	require.NoError(t, os.WriteFile(ape, []byte("APE"), 0755))

	require.NoError(t, createHostSymlinks(targets, tmpDir))

	linkTarget, err := os.Readlink(filepath.Join(tmpDir, hostLinkName("mytool_host")))
	assert.Nil(t, err)
	assert.Equal(t, "mytool", linkTarget)

	st, err := os.Lstat(ape)
	require.NoError(t, err)
	assert.Zero(t, st.Mode()&os.ModeSymlink, "the APE must stay a real file")
	body, err := os.ReadFile(ape)
	require.NoError(t, err)
	assert.Equal(t, "APE", string(body))
}

func TestCreateHostSymlinksSkipsMissing(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	targets := []build.Target{
		{ImportPath: "./cmd/mytool", OutputName: "mytool"},
	}

	// Don't create the host binary — should skip without error
	err := createHostSymlinks(targets, tmpDir)
	assert.Nil(t, err)

	// Symlinks should not exist
	_, err = os.Readlink(filepath.Join(tmpDir, "mytool_host"))
	assert.NotNil(t, err)
	_, err = os.Readlink(filepath.Join(tmpDir, "mytool"))
	assert.NotNil(t, err)
}

func TestCreateHostSymlinksReplacesStale(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	targets := []build.Target{
		{ImportPath: "./cmd/mytool", OutputName: "mytool"},
	}

	// A previous run's APE, and a stale link left over from before it existed.
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "mytool"), []byte("APE"), 0755))
	require.NoError(t, os.Symlink("old_target", filepath.Join(tmpDir, hostLinkName("mytool_host"))))

	require.NoError(t, createHostSymlinks(targets, tmpDir))

	linkTarget, err := os.Readlink(filepath.Join(tmpDir, hostLinkName("mytool_host")))
	require.NoError(t, err)
	assert.Equal(t, "mytool", linkTarget)
}

// TestRunBuildMovesOutputIntoPlace pins the write-then-move contract from
// outside runBuild: the -o arg carries the .tmp- spelling, the result ends up
// on the target file, and the temp name is gone.
func TestRunBuildMovesOutputIntoPlace(t *testing.T) {
	t.Parallel()
	final := filepath.Join(t.TempDir(), "mytool")
	var built string
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		for i, arg := range cfg.Args {
			if arg == "-o" && i+1 < len(cfg.Args) {
				built = cfg.Args[i+1]
			}
		}
		require.Equal(t, build.TempOutputPath(final), built, "-o must carry the temp spelling")
		require.NoError(t, os.WriteFile(built, []byte("BIN"), 0o755))
		return runner.MockProcess(nil, nil), nil
	}

	require.NoError(t, runBuild(mock, wasmJob(t, final), nil))

	body, err := os.ReadFile(final)
	require.NoError(t, err)
	assert.Equal(t, "BIN", string(body), "the target file holds what the build wrote")
	assert.NoFileExists(t, build.TempOutputPath(final), "the temp spelling must not survive the commit")
}

// TestRunBuildDeletesTempOutputOnFailure: a failed build leaves nothing —
// what the compiler already wrote under the temp spelling is removed, and the
// target file never appears.
func TestRunBuildDeletesTempOutputOnFailure(t *testing.T) {
	t.Parallel()
	final := filepath.Join(t.TempDir(), "mytool")
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if isGoBuild(cfg) {
			writeMockBuildOutput(cfg, "PARTIAL")
			return runner.MockProcessWithStderr(nil, []byte("./main.go:5:3: undefined: foo\n"), fmt.Errorf("exit status 1")), nil
		}
		return nil, nil
	}

	err := runBuild(mock, wasmJob(t, final), nil)
	require.NotNil(t, err)
	assert.Contains(t, err.Error(), "undefined: foo")
	assert.NoFileExists(t, build.TempOutputPath(final), "the failed build's temp output must be deleted")
	assert.NoFileExists(t, final, "a failed build must not produce the target file")
}

// TestRunBuildRefusesToCommitMissingOutput: go build reporting success without
// producing its -o target is not shippable — the run fails loudly instead of
// reporting a build whose output nobody can find.
func TestRunBuildRefusesToCommitMissingOutput(t *testing.T) {
	t.Parallel()
	final := filepath.Join(t.TempDir(), "mytool")
	// A build that "succeeds" but writes nothing, unlike a real compiler.
	mock := runner.NewMock()

	assert.Error(t, runBuild(mock, wasmJob(t, final), nil))
	assert.NoFileExists(t, final)
}
