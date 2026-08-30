package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("go.mod", []byte("module test\n\ngo 1.24.11\n"), 0644)
	v, err := requiredGoVersion()
	assert.Nil(t, err)
	assert.Equal(t, "1.24.11", v)
}

func TestRequiredGoVersionToolchainDirective(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("go.mod", []byte("module test\n\ngo 1.24.0\n\ntoolchain go1.25.0\n"), 0644)
	v, err := requiredGoVersion()
	assert.Nil(t, err)
	assert.Equal(t, "1.25.0", v) // toolchain directive takes precedence
}

func TestRequiredGoVersionTwoParts(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("go.mod", []byte("module test\n\ngo 1.25\n"), 0644)
	v, err := requiredGoVersion()
	assert.Nil(t, err)
	assert.Equal(t, "1.25.0", v)
}

func TestNormalizeGoVersion(t *testing.T) {
	assert.Equal(t, "1.25.0", normalizeGoVersion("1.25"))
	assert.Equal(t, "1.24.11", normalizeGoVersion("1.24.11"))
	assert.Equal(t, "1.25.1", normalizeGoVersion("1.25.1"))
}

func TestRequiredGoVersionNoGoDirective(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("go.mod", []byte("module test\n"), 0644)
	v, err := requiredGoVersion()
	assert.Nil(t, err)
	assert.Equal(t, "", v)
}

func TestRequiredGoVersionNoMod(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	v, err := requiredGoVersion()
	assert.NotNil(t, err)
	assert.Equal(t, "", v)
}

func TestInstalledGoVersion(t *testing.T) {
	v, err := installedGoVersion()
	assert.Nil(t, err)
	assert.NotEmpty(t, v)
	// Should be parseable as semver
	assert.Contains(t, v, ".")
}

// The fork reports its own version, which is not semver: the comparison has to
// read the numeric part or every go.mod check silently passes.
func TestGoVersionCore(t *testing.T) {
	assert.Equal(t, "1.27.0", goVersionCore("1.27.0cosmo.r685"))
	assert.Equal(t, "1.24.7", goVersionCore("1.24.7"))
	assert.Equal(t, "1.27", goVersionCore("1.27rc1"))
}

// A fork older than the module's go directive has no fallback to hide behind:
// there is no other toolchain, so this fails and names the repair.
func TestForkSatisfiesGoMod(t *testing.T) {
	dir := t.TempDir()
	oldWd, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer os.Chdir(oldWd)
	require.NoError(t, os.WriteFile("go.mod", []byte("module example.com/x\n\ngo 1.30.0\n"), 0644))

	err := forkSatisfiesGoMod("1.27.0cosmo.r685")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1.30.0")
	assert.Contains(t, err.Error(), cosmoBranchEnv)
	assert.NotContains(t, err.Error(), "go.dev", "the repair is a newer fork, never a stock Go")

	assert.NoError(t, forkSatisfiesGoMod("1.31.0cosmo.r1"))
}

func TestGoCacheDir(t *testing.T) {
	dir, err := goCacheDir()
	assert.Nil(t, err)
	assert.True(t, filepath.IsAbs(dir))
	assert.Contains(t, dir, "go-toolchain")

	// Directory should exist
	info, err := os.Stat(dir)
	assert.Nil(t, err)
	assert.True(t, info.IsDir())
}

// createTestTarGz builds a tar.gz archive in memory with the given files.
func createTestTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0755,
			Size: int64(len(content)),
		}
		require.NoError(t, tw.WriteHeader(hdr))
		_, err := tw.Write([]byte(content))
		require.Nil(t, err)
	}
	tw.Close()
	gw.Close()
	return buf.Bytes()
}

func TestExtractTarGz(t *testing.T) {
	tmpDir := t.TempDir()

	archive := createTestTarGz(t, map[string]string{
		"go/bin/go":      "#!/bin/sh\necho go",
		"go/src/main.go": "package main",
	})

	err := extractTarGz(bytes.NewReader(archive), tmpDir)
	assert.Nil(t, err)

	// Verify files were extracted
	content, err := os.ReadFile(filepath.Join(tmpDir, "go", "bin", "go"))
	assert.Nil(t, err)
	assert.Equal(t, "#!/bin/sh\necho go", string(content))

	content, err = os.ReadFile(filepath.Join(tmpDir, "go", "src", "main.go"))
	assert.Nil(t, err)
	assert.Equal(t, "package main", string(content))
}

func TestExtractTarGzWithDir(t *testing.T) {
	tmpDir := t.TempDir()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// Add a directory entry
	tw.WriteHeader(&tar.Header{Name: "go/", Typeflag: tar.TypeDir, Mode: 0755})
	tw.WriteHeader(&tar.Header{Name: "go/bin/", Typeflag: tar.TypeDir, Mode: 0755})
	// Add a regular file
	content := []byte("binary")
	tw.WriteHeader(&tar.Header{Name: "go/bin/go", Typeflag: tar.TypeReg, Mode: 0755, Size: int64(len(content))})
	tw.Write(content)
	// Add a symlink
	tw.WriteHeader(&tar.Header{Name: "go/bin/link", Typeflag: tar.TypeSymlink, Linkname: "go"})

	tw.Close()
	gw.Close()

	err := extractTarGz(bytes.NewReader(buf.Bytes()), tmpDir)
	assert.Nil(t, err)

	// Check directory
	info, err := os.Stat(filepath.Join(tmpDir, "go", "bin"))
	assert.Nil(t, err)
	assert.True(t, info.IsDir())

	// Check file
	data, err := os.ReadFile(filepath.Join(tmpDir, "go", "bin", "go"))
	assert.Nil(t, err)
	assert.Equal(t, "binary", string(data))

	// Check symlink
	target, err := os.Readlink(filepath.Join(tmpDir, "go", "bin", "link"))
	assert.Nil(t, err)
	assert.Equal(t, "go", target)
}

func TestExtractTarGzInvalidGzip(t *testing.T) {
	err := extractTarGz(bytes.NewReader([]byte("not gzip")), t.TempDir())
	assert.NotNil(t, err)
}

func TestExtractTarGzPathTraversal(t *testing.T) {
	tmpDir := t.TempDir()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// Attempt path traversal
	content := []byte("malicious")
	tw.WriteHeader(&tar.Header{Name: "../../../etc/evil", Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len(content))})
	tw.Write(content)
	tw.Close()
	gw.Close()

	err := extractTarGz(bytes.NewReader(buf.Bytes()), tmpDir)
	assert.Nil(t, err) // should not error, just skip

	// File should NOT exist outside tmpDir
	_, err = os.Stat(filepath.Join(tmpDir, "..", "..", "..", "etc", "evil"))
	assert.True(t, os.IsNotExist(err))
}

// The whole pipeline compiles with the fork, so the bootstrap's job is to put
// THAT GOROOT in front of whatever Go the host carries -- and to pin
// GOTOOLCHAIN, the setting that otherwise lets the go command fetch a stock
// toolchain behind our back to satisfy a go directive.
func TestEnsureGoVersionUsesTheForkAndPinsGOTOOLCHAIN(t *testing.T) {
	forkRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(forkRoot, "bin"), 0755))
	writeFakeGoBin(t, filepath.Join(forkRoot, "bin", "go"))

	// t.Setenv, so the GOROOT this assigns cannot outlive the test.
	t.Setenv("PATH", os.Getenv("PATH"))
	t.Setenv("GOROOT", "")
	t.Setenv("GOTOOLCHAIN", "auto")

	oldEnsure, oldVerify := ensureCosmoToolchainFunc, verifyGoToolchainFunc
	ensureCosmoToolchainFunc = func() (string, error) { return forkRoot, nil }
	verifyGoToolchainFunc = func(string) error { return nil }
	defer func() { ensureCosmoToolchainFunc, verifyGoToolchainFunc = oldEnsure, oldVerify }()

	require.NoError(t, EnsureGoVersion())

	assert.Equal(t, forkRoot, os.Getenv("GOROOT"))
	assert.Equal(t, "local", os.Getenv("GOTOOLCHAIN"))
	assert.True(t, strings.HasPrefix(os.Getenv("PATH"), filepath.Join(forkRoot, "bin")),
		"the fork's bin must come first, or the host's own go wins")
}

// No fork, no build: there is nothing else that may compile this module.
func TestEnsureGoVersionFailsWithoutTheFork(t *testing.T) {
	oldEnsure := ensureCosmoToolchainFunc
	ensureCosmoToolchainFunc = func() (string, error) { return "", fmt.Errorf("no toolchain published") }
	defer func() { ensureCosmoToolchainFunc = oldEnsure }()

	err := EnsureGoVersion()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gosmopolitan toolchain is the only compiler")
	assert.Contains(t, err.Error(), "no toolchain published")
}

// writeFakeGoBin writes a stub `go` that answers the version probe, so the
// bootstrap can be exercised without a real toolchain.
func writeFakeGoBin(t *testing.T, path string) {
	t.Helper()
	script := "#!/bin/sh\necho 'go version go1.27.0cosmo.r685 linux/amd64'\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0755))
}

func TestVerifyGoToolchainHealthy(t *testing.T) {
	goPath, err := exec.LookPath("go")
	require.NoError(t, err)

	// A healthy toolchain must pass the probe quickly; this is the happy path.
	assert.NoError(t, verifyGoToolchain(goPath))
}

func TestVerifyGoToolchainBrokenGOROOT(t *testing.T) {
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

			// go still runs and reports a version, but "go list runtime" fails here.
			t.Setenv("GOROOT", brokenRoot)

			err := verifyGoToolchain(goPath)
			require.Error(t, err)
			// Confirms we reproduce the real failure mode, not some unrelated error.
			assert.Contains(t, err.Error(), "runtime")
		})
	}
}

// A broken fork is a failed run, not a quiet swap to whatever Go is lying
// around: the swap is what this whole change exists to prevent.
func TestEnsureGoVersionBrokenForkFails(t *testing.T) {
	forkRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(forkRoot, "bin"), 0755))
	writeFakeGoBin(t, filepath.Join(forkRoot, "bin", "go"))

	// t.Setenv, so the GOROOT this assigns cannot outlive the test.
	t.Setenv("PATH", os.Getenv("PATH"))
	t.Setenv("GOROOT", os.Getenv("GOROOT"))
	t.Setenv("GOTOOLCHAIN", os.Getenv("GOTOOLCHAIN"))

	oldEnsure, oldVerify := ensureCosmoToolchainFunc, verifyGoToolchainFunc
	ensureCosmoToolchainFunc = func() (string, error) { return forkRoot, nil }
	verifyGoToolchainFunc = func(string) error {
		return fmt.Errorf("package runtime is not in std")
	}
	defer func() { ensureCosmoToolchainFunc, verifyGoToolchainFunc = oldEnsure, oldVerify }()

	err := EnsureGoVersion()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed its integrity probe")
}

func TestRecordGoMinor(t *testing.T) {
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
