package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeFingerprint(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.WriteFile("main.go", []byte("package main\n\nfunc main() {}\n"), 0644)

	fp1, err := computeFingerprint()
	require.NoError(t, err)
	assert.NotEmpty(t, fp1)

	// Same inputs -> same fingerprint
	fp2, err := computeFingerprint()
	require.NoError(t, err)
	assert.Equal(t, fp1, fp2)

	// Change a file -> different fingerprint
	os.WriteFile("main.go", []byte("package main\n\nfunc main() { println(\"hi\") }\n"), 0644)
	fp3, err := computeFingerprint()
	require.NoError(t, err)
	assert.NotEqual(t, fp1, fp3)
}

func TestComputeFingerprintIncludesGoSum(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.WriteFile("main.go", []byte("package main\n"), 0644)

	fp1, err := computeFingerprint()
	require.NoError(t, err)

	// Adding go.sum changes fingerprint
	os.WriteFile("go.sum", []byte("example.com/dep v1.0.0 h1:abc=\n"), 0644)
	fp2, err := computeFingerprint()
	require.NoError(t, err)
	assert.NotEqual(t, fp1, fp2)
}

func TestComputeFingerprintSkipsBuildDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.WriteFile("main.go", []byte("package main\n"), 0644)

	fp1, err := computeFingerprint()
	require.NoError(t, err)

	// Adding files in build/ should not change fingerprint
	os.MkdirAll("build", 0755)
	os.WriteFile("build/binary.go", []byte("package main\n"), 0644)
	fp2, err := computeFingerprint()
	require.NoError(t, err)
	assert.Equal(t, fp1, fp2)
}

func TestFingerprintFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))

	fp := fingerprintFile()
	assert.Contains(t, fp, "go-toolchain-fingerprint")
	assert.True(t, filepath.IsAbs(fp))
}

func TestIsUpToDateNoFingerprint(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.WriteFile("main.go", []byte("package main\n"), 0644)

	// No stored fingerprint -> not up to date
	assert.False(t, isUpToDate())
}

func TestIsUpToDateWithMatchingFingerprint(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.MkdirAll("src", 0755)
	os.WriteFile("src/main.go", []byte("package main\nfunc main() {}\n"), 0644)

	// Create build output
	os.MkdirAll("build", 0755)
	os.WriteFile("build/example.com", []byte("binary"), 0755)

	// Save fingerprint
	saveFingerprint()

	// Should be up to date now
	assert.True(t, isUpToDate())
}

func TestIsUpToDateStaleAfterChange(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.MkdirAll("src", 0755)
	os.WriteFile("src/main.go", []byte("package main\nfunc main() {}\n"), 0644)

	// Create build output
	os.MkdirAll("build", 0755)
	os.WriteFile("build/example.com", []byte("binary"), 0755)

	saveFingerprint()

	// Modify source -> stale
	os.WriteFile("src/main.go", []byte("package main\nfunc main() { println(\"changed\") }\n"), 0644)
	assert.False(t, isUpToDate())
}

func TestSaveFingerprint(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.WriteFile("main.go", []byte("package main\n"), 0644)

	saveFingerprint()

	fp := fingerprintFile()
	data, err := os.ReadFile(fp)
	require.NoError(t, err)
	assert.NotEmpty(t, data)
	assert.Len(t, string(data), 64) // SHA-256 hex = 64 chars
}
