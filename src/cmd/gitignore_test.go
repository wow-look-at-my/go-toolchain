package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitignoreContains_ExactMatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	os.WriteFile(path, []byte("/build/\n"), 0644)

	assert.True(t, gitignoreContains(path, "/build/"))
}

func TestGitignoreContains_WithoutLeadingSlash(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	os.WriteFile(path, []byte("build/\n"), 0644)

	assert.True(t, gitignoreContains(path, "/build/"))
}

func TestGitignoreContains_WithoutTrailingSlash(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	os.WriteFile(path, []byte("build\n"), 0644)

	assert.True(t, gitignoreContains(path, "/build/"))
}

func TestGitignoreContains_NotPresent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	os.WriteFile(path, []byte("vendor/\nbin/\n"), 0644)

	assert.False(t, gitignoreContains(path, "/build/"))
}

func TestGitignoreContains_IgnoresComments(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	os.WriteFile(path, []byte("# build/\n"), 0644)

	assert.False(t, gitignoreContains(path, "/build/"))
}

func TestGitignoreContains_MissingFile(t *testing.T) {
	t.Parallel()
	assert.False(t, gitignoreContains("/nonexistent/.gitignore", "/build/"))
}

func TestEnsureBuildDirInGitignore_AddsEntry(t *testing.T) {
	t.Serial()
	dir := t.TempDir()

	// Create a git repo
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0755))

	// Create an empty .gitignore
	gitignorePath := filepath.Join(dir, ".gitignore")
	os.WriteFile(gitignorePath, []byte("vendor/\n"), 0644)

	// Run from inside the repo
	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	os.Chdir(dir)

	oldOutputDir := outputDir
	defer func() { outputDir = oldOutputDir }()
	outputDir = "build"

	ensureBuildDirInGitignore()

	content, err := os.ReadFile(gitignorePath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "/build/")
}

func TestEnsureBuildDirInGitignore_AlreadyPresent(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0755))

	gitignorePath := filepath.Join(dir, ".gitignore")
	os.WriteFile(gitignorePath, []byte("/build/\n"), 0644)

	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	os.Chdir(dir)

	oldOutputDir := outputDir
	defer func() { outputDir = oldOutputDir }()
	outputDir = "build"

	ensureBuildDirInGitignore()

	content, err := os.ReadFile(gitignorePath)
	require.NoError(t, err)
	// Should not duplicate
	assert.Equal(t, "/build/\n", string(content))
}

func TestEnsureBuildDirInGitignore_NoGitRepo(t *testing.T) {
	t.Serial()
	dir := t.TempDir()

	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	os.Chdir(dir)

	// Should not panic or create any files
	ensureBuildDirInGitignore()

	_, err := os.Stat(filepath.Join(dir, ".gitignore"))
	assert.True(t, os.IsNotExist(err))
}

func TestEnsureBuildDirInGitignore_CreatesGitignore(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0755))

	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	os.Chdir(dir)

	oldOutputDir := outputDir
	defer func() { outputDir = oldOutputDir }()
	outputDir = "build"

	ensureBuildDirInGitignore()

	content, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	require.NoError(t, err)
	assert.Equal(t, "/build/\n", string(content))
}

func TestEnsureBuildDirInGitignore_NoTrailingNewline(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0755))

	gitignorePath := filepath.Join(dir, ".gitignore")
	os.WriteFile(gitignorePath, []byte("vendor/"), 0644) // no trailing newline

	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	os.Chdir(dir)

	oldOutputDir := outputDir
	defer func() { outputDir = oldOutputDir }()
	outputDir = "build"

	ensureBuildDirInGitignore()

	content, err := os.ReadFile(gitignorePath)
	require.NoError(t, err)
	assert.Equal(t, "vendor/\n/build/\n", string(content))
}

func TestNeedsLeadingNewline(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// File ending with newline
	withNL := filepath.Join(dir, "with_nl")
	os.WriteFile(withNL, []byte("hello\n"), 0644)
	assert.False(t, needsLeadingNewline(withNL))

	// File without trailing newline
	withoutNL := filepath.Join(dir, "without_nl")
	os.WriteFile(withoutNL, []byte("hello"), 0644)
	assert.True(t, needsLeadingNewline(withoutNL))

	// Empty file
	empty := filepath.Join(dir, "empty")
	os.WriteFile(empty, []byte(""), 0644)
	assert.False(t, needsLeadingNewline(empty))

	// Missing file
	assert.False(t, needsLeadingNewline(filepath.Join(dir, "missing")))
}

func TestRemoveFromGitignore_RemovesGuardLine(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0755))
	gitignorePath := filepath.Join(dir, ".gitignore")
	require.NoError(t, os.WriteFile(gitignorePath, []byte("/build/\ngomemlimit_gen.go\nvendor/\n"), 0644))

	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	require.NoError(t, os.Chdir(dir))

	removeFromGitignore("gomemlimit_gen.go")

	content, err := os.ReadFile(gitignorePath)
	require.NoError(t, err)
	assert.Equal(t, "/build/\nvendor/\n", string(content), "only the guard line is dropped; other entries and the trailing newline survive")
}

func TestRemoveFromGitignore_NoOpWhenAbsent(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0755))
	gitignorePath := filepath.Join(dir, ".gitignore")
	const original = "/build/\nvendor/\n"
	require.NoError(t, os.WriteFile(gitignorePath, []byte(original), 0644))

	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	require.NoError(t, os.Chdir(dir))

	removeFromGitignore("gomemlimit_gen.go")

	content, err := os.ReadFile(gitignorePath)
	require.NoError(t, err)
	assert.Equal(t, original, string(content), "an absent entry leaves the file byte-identical (no rewrite)")
}

// TestEnsureBuildDirInGitignore_StripsStaleGuard verifies the migration: a
// .gitignore an older go-toolchain polluted with the guard line is cleaned up
// while the build-dir entry is still ensured.
func TestEnsureBuildDirInGitignore_StripsStaleGuard(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0755))
	gitignorePath := filepath.Join(dir, ".gitignore")
	require.NoError(t, os.WriteFile(gitignorePath, []byte("/build/\ngomemlimit_gen.go\n"), 0644))

	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	require.NoError(t, os.Chdir(dir))

	oldOutputDir := outputDir
	defer func() { outputDir = oldOutputDir }()
	outputDir = "build"

	ensureBuildDirInGitignore()

	content, err := os.ReadFile(gitignorePath)
	require.NoError(t, err)
	assert.NotContains(t, string(content), "gomemlimit_gen.go", "stale guard line must be stripped")
	assert.Contains(t, string(content), "/build/", "build dir entry is preserved")
}
