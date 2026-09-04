package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitignoreContains_ExactMatch(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	os.WriteFile(path, []byte("/build/\n"), 0644)

	assert.True(t, gitignoreContains(path, "/build/"))
}

func TestGitignoreContains_WithoutLeadingSlash(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	os.WriteFile(path, []byte("build/\n"), 0644)

	assert.True(t, gitignoreContains(path, "/build/"))
}

func TestGitignoreContains_WithoutTrailingSlash(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	os.WriteFile(path, []byte("build\n"), 0644)

	assert.True(t, gitignoreContains(path, "/build/"))
}

func TestGitignoreContains_NotPresent(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	os.WriteFile(path, []byte("vendor/\nbin/\n"), 0644)

	assert.False(t, gitignoreContains(path, "/build/"))
}

func TestGitignoreContains_IgnoresComments(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	os.WriteFile(path, []byte("# build/\n"), 0644)

	assert.False(t, gitignoreContains(path, "/build/"))
}

func TestGitignoreContains_MissingFile(t *testing.T) {
	t.Serial()
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
	t.Chdir(dir)

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

	t.Chdir(dir)

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

	t.Chdir(dir)

	// Should not panic or create any files
	ensureBuildDirInGitignore()

	_, err := os.Stat(filepath.Join(dir, ".gitignore"))
	assert.True(t, os.IsNotExist(err))
}

func TestEnsureBuildDirInGitignore_CreatesGitignore(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0755))

	t.Chdir(dir)

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

	t.Chdir(dir)

	oldOutputDir := outputDir
	defer func() { outputDir = oldOutputDir }()
	outputDir = "build"

	ensureBuildDirInGitignore()

	content, err := os.ReadFile(gitignorePath)
	require.NoError(t, err)
	assert.Equal(t, "vendor/\n/build/\n", string(content))
}

func TestNeedsLeadingNewline(t *testing.T) {
	t.Serial()
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

// TestEnsureBuildDirInGitignore_AddsBuildDir covers the one entry this writes:
// an existing .gitignore keeps what it holds and gains the build directory.
func TestEnsureBuildDirInGitignore_AddsBuildDir(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0755))
	gitignorePath := filepath.Join(dir, ".gitignore")
	require.NoError(t, os.WriteFile(gitignorePath, []byte("vendor/\n"), 0644))

	t.Chdir(dir)

	oldOutputDir := outputDir
	defer func() { outputDir = oldOutputDir }()
	outputDir = "build"

	ensureBuildDirInGitignore()

	content, err := os.ReadFile(gitignorePath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "/build/", "build dir entry is added")
	assert.Contains(t, string(content), "vendor/", "existing entries survive")
}
