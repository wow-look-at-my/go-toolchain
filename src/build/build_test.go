package build

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

func TestFindMainPackagesParsesFilesystem(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.MkdirAll("cmd/foo", 0755)
	os.MkdirAll("cmd/bar", 0755)
	os.MkdirAll("pkg/lib", 0755)
	os.WriteFile("cmd/foo/main.go", []byte("package main\n"), 0644)
	os.WriteFile("cmd/bar/main.go", []byte("package main\n"), 0644)
	os.WriteFile("pkg/lib/lib.go", []byte("package lib\n"), 0644)

	pkgs, err := findMainPackages()
	require.Nil(t, err)
	require.Equal(t, 2, len(pkgs))
	assert.Contains(t, pkgs, "example.com/cmd/foo")
	assert.Contains(t, pkgs, "example.com/cmd/bar")
}

func TestFindMainPackagesEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.MkdirAll("pkg/lib", 0755)
	os.WriteFile("pkg/lib/lib.go", []byte("package lib\n"), 0644)

	pkgs, err := findMainPackages()
	require.Nil(t, err)
	assert.Equal(t, 0, len(pkgs))
}

func TestFindMainPackagesSkipsTestFiles(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	// Only a _test.go file declaring package main — should NOT be found
	os.WriteFile("main_test.go", []byte("package main\n"), 0644)

	pkgs, err := findMainPackages()
	require.Nil(t, err)
	assert.Equal(t, 0, len(pkgs))
}

func TestFindMainPackagesSkipsHiddenDirs(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.MkdirAll(".hidden", 0755)
	os.WriteFile(".hidden/main.go", []byte("package main\n"), 0644)

	pkgs, err := findMainPackages()
	require.Nil(t, err)
	assert.Equal(t, 0, len(pkgs))
}

func TestBinaryNameFromImportPath(t *testing.T) {
	tests := []struct {
		pkg, module, want string
	}{
		// Single level below module → use module name
		{"github.com/wow-look-at-my/go-toolchain/src", "github.com/wow-look-at-my/go-toolchain", "go-toolchain"},
		{"example.com/src", "example.com", "example.com"},
		{"mymod/app", "mymod", "mymod"},

		// Deeper path → use leaf directory
		{"example.com/cmd/foo", "example.com", "foo"},
		{"example.com/cmd/bar", "example.com", "bar"},
		{"example.com/tools/linter", "example.com", "linter"},

		// No module prefix match → fallback to basename
		{"unrelated/pkg", "example.com", "pkg"},

		// Empty module name → fallback to basename
		{"example.com/cmd/foo", "", "foo"},
	}
	for _, tt := range tests {
		got := binaryNameFromImportPath(tt.pkg, tt.module)
		assert.Equal(t, tt.want, got)
	}
}

func TestResolveBuildTargetsGoFilesInRoot(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.WriteFile("main.go", []byte("package main\n"), 0644)

	mock := runner.NewMock()
	targets, err := ResolveBuildTargets(mock)
	require.Nil(t, err)
	require.Equal(t, 1, len(targets))
	assert.Equal(t, "example.com", targets[0].ImportPath)
	assert.Equal(t, "example.com", targets[0].OutputName)
}

func TestResolveBuildTargetsAutoDetectSingle(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.MkdirAll("cmd/myapp", 0755)
	os.WriteFile("cmd/myapp/main.go", []byte("package main\n"), 0644)

	mock := runner.NewMock()
	targets, err := ResolveBuildTargets(mock)
	require.Nil(t, err)
	assert.False(t, len(targets) != 1 || targets[0].ImportPath != "example.com/cmd/myapp" || targets[0].OutputName != "myapp")
}

func TestResolveBuildTargetsAutoDetectMultiple(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.MkdirAll("cmd/foo", 0755)
	os.MkdirAll("cmd/bar", 0755)
	os.WriteFile("cmd/foo/main.go", []byte("package main\n"), 0644)
	os.WriteFile("cmd/bar/main.go", []byte("package main\n"), 0644)

	mock := runner.NewMock()
	targets, err := ResolveBuildTargets(mock)
	require.Nil(t, err)
	require.Equal(t, 2, len(targets))
	assert.Equal(t, "bar", targets[0].OutputName)
	assert.Equal(t, "foo", targets[1].OutputName)
}

func TestResolveBuildTargetsAutoDetectSrcDir(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("go.mod", []byte("module github.com/wow-look-at-my/go-toolchain\n\ngo 1.21\n"), 0644)
	os.MkdirAll("src", 0755)
	os.WriteFile("src/main.go", []byte("package main\n"), 0644)

	mock := runner.NewMock()
	targets, err := ResolveBuildTargets(mock)
	require.Nil(t, err)
	require.Equal(t, 1, len(targets))
	// Binary should be named after the module, not "src"
	assert.Equal(t, "go-toolchain", targets[0].OutputName)
}

func TestFindAllPackagesByDir(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Create directory structure with .go files
	os.MkdirAll("pkg/util", 0755)
	os.MkdirAll("internal/core", 0755)
	os.MkdirAll(".hidden", 0755)
	os.MkdirAll("vendor", 0755)
	os.MkdirAll("testdata", 0755)
	os.MkdirAll("empty", 0755)

	// Root has a .go file
	os.WriteFile("lib.go", []byte("package mylib"), 0644)
	// Subdirs with .go files
	os.WriteFile("pkg/util/util.go", []byte("package util"), 0644)
	os.WriteFile("internal/core/core.go", []byte("package core"), 0644)
	// Test-only file should not count
	os.WriteFile("empty/foo_test.go", []byte("package empty"), 0644)
	// Hidden/vendor/testdata should be skipped
	os.WriteFile(".hidden/h.go", []byte("package hidden"), 0644)
	os.WriteFile("vendor/v.go", []byte("package vendor"), 0644)
	os.WriteFile("testdata/t.go", []byte("package testdata"), 0644)

	pkgs, err := findAllPackagesByDir("example.com/mylib")
	require.Nil(t, err)

	assert.Contains(t, pkgs, "example.com/mylib")
	assert.Contains(t, pkgs, "example.com/mylib/pkg/util")
	assert.Contains(t, pkgs, "example.com/mylib/internal/core")
	assert.NotContains(t, pkgs, "example.com/mylib/.hidden")
	assert.NotContains(t, pkgs, "example.com/mylib/vendor")
	assert.NotContains(t, pkgs, "example.com/mylib/testdata")
	assert.NotContains(t, pkgs, "example.com/mylib/empty")
}

func TestResolveBuildTargetsFallsBackToAllPackages(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Create a library-only project (no main packages)
	os.WriteFile("go.mod", []byte("module example.com/mylib\n\ngo 1.21\n"), 0644)
	os.MkdirAll("pkg", 0755)
	os.WriteFile("lib.go", []byte("package mylib"), 0644)
	os.WriteFile("pkg/helper.go", []byte("package pkg"), 0644)

	mock := runner.NewMock()
	targets, err := ResolveBuildTargets(mock)
	require.Nil(t, err)
	require.Equal(t, 2, len(targets))

	names := []string{targets[0].ImportPath, targets[1].ImportPath}
	assert.Contains(t, names, "example.com/mylib")
	assert.Contains(t, names, "example.com/mylib/pkg")
}

func TestResolveBuildTargetsDeduplicates(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("go.mod", []byte("module example.com/mymod\n\ngo 1.21\n"), 0644)
	// Two main packages that resolve to the same binary name
	os.WriteFile("main.go", []byte("package main\n"), 0644)
	os.MkdirAll("src", 0755)
	os.WriteFile("src/main.go", []byte("package main\n"), 0644)

	mock := runner.NewMock()
	targets, err := ResolveBuildTargets(mock)
	require.Nil(t, err)
	// Both resolve to "mymod" — should be deduplicated to 1
	require.Equal(t, 1, len(targets))
	assert.Equal(t, "mymod", targets[0].OutputName)
}

// Verify that findMainPackages does not need the runner parameter
func TestFindMainPackagesNoRunner(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("go.mod", []byte("module example.com/test\n\ngo 1.21\n"), 0644)
	os.WriteFile("main.go", []byte("package main\n\nfunc main() {}\n"), 0644)

	pkgs, err := findMainPackages()
	require.Nil(t, err)
	require.Equal(t, 1, len(pkgs))
	assert.Equal(t, "example.com/test", pkgs[0])
}

