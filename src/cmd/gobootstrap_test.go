package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequiredGoVersion(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	os.WriteFile("go.mod", []byte("module test\n\ngo 1.24.11\n"), 0644)
	v, err := requiredGoVersion()
	assert.Nil(t, err)
	assert.Equal(t, "1.24.11", v)
}

func TestRequiredGoVersionToolchainDirective(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	os.WriteFile("go.mod", []byte("module test\n\ngo 1.24.0\n\ntoolchain go1.25.0\n"), 0644)
	v, err := requiredGoVersion()
	assert.Nil(t, err)
	assert.Equal(t, "1.25.0", v) // toolchain directive takes precedence
}

func TestRequiredGoVersionTwoParts(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	os.WriteFile("go.mod", []byte("module test\n\ngo 1.25\n"), 0644)
	v, err := requiredGoVersion()
	assert.Nil(t, err)
	assert.Equal(t, "1.25.0", v)
}

func TestNormalizeGoVersion(t *testing.T) {
	t.Serial()
	assert.Equal(t, "1.25.0", normalizeGoVersion("1.25"))
	assert.Equal(t, "1.24.11", normalizeGoVersion("1.24.11"))
	assert.Equal(t, "1.25.1", normalizeGoVersion("1.25.1"))
}

func TestRequiredGoVersionNoGoDirective(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	os.WriteFile("go.mod", []byte("module test\n"), 0644)
	v, err := requiredGoVersion()
	assert.Nil(t, err)
	assert.Equal(t, "", v)
}

func TestRequiredGoVersionNoMod(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	v, err := requiredGoVersion()
	assert.NotNil(t, err)
	assert.Equal(t, "", v)
}

func TestInstalledGoVersion(t *testing.T) {
	t.Serial()
	v, err := installedGoVersion()
	assert.Nil(t, err)
	assert.NotEmpty(t, v)
	// Should be parseable as semver
	assert.Contains(t, v, ".")
}

// The fork reports its own version, which is not semver: the comparison has to
// read the numeric part or every go.mod check silently passes.
func TestGoVersionCore(t *testing.T) {
	t.Serial()
	assert.Equal(t, "1.27.0", goVersionCore("1.27.0cosmo.r685"))
	assert.Equal(t, "1.24.7", goVersionCore("1.24.7"))
	assert.Equal(t, "1.27", goVersionCore("1.27rc1"))
}

// The list separator belongs to the host named, not to the machine joining
// them. A colon on NT fuses the link directory with the next entry into a
// directory that does not exist, so the runner's own go wins.
func TestPathWithFirst(t *testing.T) {
	t.Serial()
	got := pathWithFirst(`C:\link`, `C:\tools;C:\bin`, "windows")
	assert.Equal(t, `C:\link;C:\tools;C:\bin`, got)

	got = pathWithFirst("/link", "/usr/bin:/bin", "linux")
	assert.Equal(t, "/link:/usr/bin:/bin", got)

	assert.Equal(t, "/link", pathWithFirst("/link", "", "linux"))
}

// A fork older than the module's go directive has no fallback to hide behind:
// there is no other toolchain, so this fails and names the repair.
func TestForkSatisfiesGoMod(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("go.mod", []byte("module example.com/x\n\ngo 1.30.0\n"), 0644))

	err := forkSatisfiesGoMod("1.27.0cosmo.r685")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1.30.0")
	assert.NotContains(t, err.Error(), "go.dev", "the repair is a newer fork, never a stock Go")

	assert.NoError(t, forkSatisfiesGoMod("1.31.0cosmo.r1"))
}

func TestGoCacheDir(t *testing.T) {
	t.Serial()
	dir, err := goCacheDir()
	assert.Nil(t, err)
	assert.True(t, filepath.IsAbs(dir))
	assert.Contains(t, dir, "go-toolchain")

	// Directory should exist
	info, err := os.Stat(dir)
	assert.Nil(t, err)
	assert.True(t, info.IsDir())
}

// The whole pipeline compiles with the go command this binary is, so the
// setup's job is to put a link to it named go in front of whatever Go the
// host carries, to point GOROOT at the standard library it builds against,
// and to pin GOTOOLCHAIN, the setting that otherwise lets the go command
// fetch a stock toolchain behind our back to satisfy a go directive.
func TestEnsureGoVersionLinksItselfAsGoAndPinsGOTOOLCHAIN(t *testing.T) {
	t.Serial()
	requireShebangHelper(t)
	// Outside this module, so the fork checkout is not the GOROOT.
	t.Chdir(t.TempDir())
	exe := filepath.Join(t.TempDir(), "go-toolchain")
	writeFakeGoBin(t, exe)

	// t.Setenv, so the GOROOT this assigns cannot outlive the test.
	t.Setenv("PATH", os.Getenv("PATH"))
	t.Setenv("GOROOT", "")
	t.Setenv("GOTOOLCHAIN", "auto")

	oldExe, oldVerify := selfExecutableFunc, verifyGoToolchainFunc
	oldCmd, oldRoot := activeGoCmd, activeGoroot
	selfExecutableFunc = func() (string, error) { return exe, nil }
	verifyGoToolchainFunc = func(string) error { return nil }
	t.Cleanup(func() {
		selfExecutableFunc, verifyGoToolchainFunc = oldExe, oldVerify
		activeGoCmd, activeGoroot = oldCmd, oldRoot
		removeGoLink()
	})

	require.NoError(t, EnsureGoVersion())

	assert.Equal(t, exe, os.Getenv("GOROOT"), "outside this module the executable carries the standard library")
	assert.Equal(t, "local", os.Getenv("GOTOOLCHAIN"))
	assert.Equal(t, []string{exe, "go"}, activeGoCmd)
	assert.True(t, strings.HasPrefix(os.Getenv("PATH"), goLinkDir), "the go link must come first, or the host's own go wins")
	link, err := os.Readlink(filepath.Join(goLinkDir, "go"))
	require.NoError(t, err)
	assert.Equal(t, exe, link)
}

// A go command that cannot compile is a failed run, never a quiet swap to
// whatever Go is lying around.
func TestEnsureGoVersionFailsWhenTheProbeFails(t *testing.T) {
	t.Serial()
	requireShebangHelper(t)
	t.Chdir(t.TempDir())
	exe := filepath.Join(t.TempDir(), "go-toolchain")
	writeFakeGoBin(t, exe)
	t.Setenv("PATH", os.Getenv("PATH"))
	t.Setenv("GOROOT", os.Getenv("GOROOT"))
	t.Setenv("GOTOOLCHAIN", os.Getenv("GOTOOLCHAIN"))

	oldExe, oldVerify := selfExecutableFunc, verifyGoToolchainFunc
	oldCmd, oldRoot := activeGoCmd, activeGoroot
	selfExecutableFunc = func() (string, error) { return exe, nil }
	verifyGoToolchainFunc = func(string) error { return fmt.Errorf("package runtime is not in std") }
	t.Cleanup(func() {
		selfExecutableFunc, verifyGoToolchainFunc = oldExe, oldVerify
		activeGoCmd, activeGoroot = oldCmd, oldRoot
		removeGoLink()
	})

	err := EnsureGoVersion()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed its integrity probe")
}

// writeFakeGoBin writes a stub `go` that answers the version probe, so the
// setup can be exercised without a real toolchain.
func writeFakeGoBin(t *testing.T, path string) {
	t.Helper()
	script := "#!/bin/sh\necho 'go version go1.27.0cosmo.r685 linux/amd64'\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0755))
}

func TestVerifyGoToolchainHealthy(t *testing.T) {
	t.Serial()
	goPath, err := exec.LookPath("go")
	require.NoError(t, err)

	// A healthy toolchain must pass the probe quickly; this is the happy path.
	assert.NoError(t, verifyGoToolchain(goPath))
}

// The caller's build flags stay out of the probe. GOFLAGS=-race asks this
// pipeline for a race-checked run, and cosmo refuses the detector.
func TestVerifyGoToolchainIgnoresTheCallersGOFLAGS(t *testing.T) {
	t.Serial()
	goPath, err := exec.LookPath("go")
	require.NoError(t, err)

	t.Setenv("GOFLAGS", "-race")
	assert.NoError(t, verifyGoToolchain(goPath))
}

// This binary is the go command, and it carries the standard library it
// compiles. The tree GOROOT names decides nothing about whether runtime
// resolves. A half-extracted GOROOT therefore stops no build, and the probe
// answers from what the binary carries.
func TestVerifyGoToolchainReadsWhatTheBinaryCarries(t *testing.T) {
	t.Serial()
	goPath, err := exec.LookPath("go")
	require.NoError(t, err)

	cases := []struct {
		name string
		// setup populates a fake GOROOT under root; the dir is set via GOROOT env.
		setup func(t *testing.T, root string)
	}{
		{
			name: "missing runtime",
			setup: func(t *testing.T, root string) {
				// Has src/ but not src/runtime: the half-extracted hosted-tool-cache case.
				require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
			},
		},
		{
			name: "garbled runtime",
			setup: func(t *testing.T, root string) {
				// src/runtime exists but its sources are not valid Go.
				rt := filepath.Join(root, "src", "runtime")
				require.NoError(t, os.MkdirAll(rt, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(rt, "pool.go"), []byte("not valid go source"), 0o644))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			brokenRoot := t.TempDir()
			tc.setup(t, brokenRoot)

			t.Setenv("GOROOT", brokenRoot)

			assert.NoError(t, verifyGoToolchain(goPath))
		})
	}
}

func TestRecordGoMinor(t *testing.T) {
	t.Serial()
	old := resolvedGoMinor
	defer func() { resolvedGoMinor = old }()

	resolvedGoMinor = 0
	recordGoMinor("1.24.7")
	assert.Equal(t, 24, resolvedGoMinor)

	resolvedGoMinor = 0
	recordGoMinor("1.25.0")
	assert.Equal(t, 25, resolvedGoMinor)

	resolvedGoMinor = 0
	recordGoMinor("1.25")
	assert.Equal(t, 25, resolvedGoMinor)
}
