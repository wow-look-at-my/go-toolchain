package test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

func TestParseProfile(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	coverFile := filepath.Join(tmpDir, "coverage.out")

	// Build test data with known coverage: file1 partly covered, file2 fully
	// covered, and a statement-weighted total across both.
	lines := []struct {
		file       string
		statements int
		covered    bool
	}{
		{"example.com/pkg/file1.go:10.20,12.2", 1, true},
		{"example.com/pkg/file1.go:14.20,16.2", 1, false},
		{"example.com/pkg/file2.go:10.20,12.2", 1, true},
		{"example.com/pkg/file2.go:14.20,16.2", 1, true},
	}

	var totalStmts, coveredStmts int
	content := "mode: set\n"
	for _, l := range lines {
		count := 0
		if l.covered {
			count = 1
			coveredStmts += l.statements
		}
		totalStmts += l.statements
		content += fmt.Sprintf("%s %d %d\n", l.file, l.statements, count)
	}

	expectedTotal := float32(coveredStmts) / float32(totalStmts) * 100

	require.NoError(t, os.WriteFile(coverFile, []byte(content), 0644))

	total, files, err := ParseProfile(coverFile)
	require.Nil(t, err)

	assert.Equal(t, expectedTotal, total)

	assert.Equal(t, 2, len(files))
}

func TestParseProfileMissingFile(t *testing.T) {
	t.Serial()
	_, _, err := ParseProfile("/nonexistent/coverage.out")
	assert.NotNil(t, err)
}

func TestParseProfileEmpty(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	coverFile := filepath.Join(tmpDir, "coverage.out")

	content := `mode: set
`
	require.NoError(t, os.WriteFile(coverFile, []byte(content), 0644))

	total, files, err := ParseProfile(coverFile)
	require.Nil(t, err)

	assert.Equal(t, float32(0), total)

	assert.Equal(t, 0, len(files))
}

func TestParseProfileMalformedLines(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	coverFile := filepath.Join(tmpDir, "coverage.out")

	// Various malformed lines that should be skipped
	content := `mode: set
not enough fields
too many fields here now extra
no-colon-in-path 1 1
example.com/pkg/file.go:10.20,12.2 1 1
`
	require.NoError(t, os.WriteFile(coverFile, []byte(content), 0644))

	total, files, err := ParseProfile(coverFile)
	require.Nil(t, err)

	// Should only have parsed the valid line
	assert.Equal(t, 1, len(files))
	assert.Equal(t, float32(100), total)
}

func TestParseProfileMergesDuplicates(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	coverFile := filepath.Join(tmpDir, "coverage.out")

	// Simulate a recent Go's -coverpkg=./... output, where each block reappears
	// per test package. The leading block is covered by a single test package,
	// the block below it is never covered.
	content := `mode: set
example.com/pkg/file1.go:10.20,12.2 1 0
example.com/pkg/file1.go:10.20,12.2 1 0
example.com/pkg/file1.go:10.20,12.2 1 1
example.com/pkg/file1.go:14.20,16.2 1 0
example.com/pkg/file1.go:14.20,16.2 1 0
example.com/pkg/file1.go:14.20,16.2 1 0
`
	require.NoError(t, os.WriteFile(coverFile, []byte(content), 0644))

	total, files, err := ParseProfile(coverFile)
	require.NoError(t, err)

	// After merging, the duplicates collapse into the unique blocks
	assert.Equal(t, float32(50), total)
	assert.Equal(t, 1, len(files))
	assert.Equal(t, 2, files[0].Statements)
	assert.Equal(t, 1, files[0].Covered)
}

func TestFilterBlocksByReachable(t *testing.T) {
	t.Serial()
	blocks := []coverageBlock{
		{file: "example.com/pkg1/file.go", statements: 2, count: 1},
		{file: "example.com/pkg2/file.go", statements: 3, count: 0},
		{file: "example.com/pkg3/file.go", statements: 1, count: 1},
	}

	reachable := set.Of("example.com/pkg1", "example.com/pkg3")

	filtered := filterBlocksByReachable(blocks, reachable)
	assert.Equal(t, 2, len(filtered))
	assert.Equal(t, "example.com/pkg1/file.go", filtered[0].file)
	assert.Equal(t, "example.com/pkg3/file.go", filtered[1].file)
}

func TestFilterBlocksByReachableNil(t *testing.T) {
	t.Serial()
	blocks := []coverageBlock{
		{file: "example.com/pkg1/file.go", statements: 2, count: 1},
		{file: "example.com/pkg2/file.go", statements: 3, count: 0},
	}

	// an unset set should return all blocks
	filtered := filterBlocksByReachable(blocks, set.Set[string]{})
	assert.Equal(t, 2, len(filtered))

	// empty reachable should also return all blocks
	filtered = filterBlocksByReachable(blocks, set.New[string]())
	assert.Equal(t, 2, len(filtered))
}

func TestParseProfileFiltered(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	coverFile := filepath.Join(tmpDir, "coverage.out")

	// pkg1 and pkg3 are covered, pkg2 is not; only pkg1 and pkg3 are reachable
	content := `mode: set
example.com/pkg1/file.go:10.20,12.2 1 1
example.com/pkg2/file.go:10.20,12.2 1 0
example.com/pkg3/file.go:10.20,12.2 1 1
`
	require.NoError(t, os.WriteFile(coverFile, []byte(content), 0644))

	reachable := set.Of("example.com/pkg1", "example.com/pkg3")

	total, files, err := ParseProfileFiltered(coverFile, reachable)
	require.NoError(t, err)

	// Should only include pkg1 and pkg3, which are fully covered
	assert.Equal(t, float32(100), total)
	assert.Equal(t, 2, len(files))

	// Without filtering, the uncovered package drags the total down
	totalUnfiltered, filesUnfiltered, err := ParseProfile(coverFile)
	require.NoError(t, err)
	assert.Equal(t, 3, len(filesUnfiltered))
	assert.InDelta(t, 66.67, float64(totalUnfiltered), 0.1)
}

func TestReachablePackages(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	// Set up filesystem: go.mod + a main package in cmd/app
	os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module example.com/mymod\n\ngo 1.21\n"), 0644)
	os.MkdirAll(filepath.Join(tmpDir, "cmd", "app"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "cmd", "app", "main.go"), []byte("package main\n"), 0644)

	mock := newMockRunnerForReachable(
		"fmt\nexample.com/mymod/pkg1\nexample.com/mymod/pkg2\nstrings\n")

	reachable, err := ReachablePackages(tmpDir, mock)
	require.NoError(t, err)

	assert.True(t, reachable.Contains("example.com/mymod/pkg1"))
	assert.True(t, reachable.Contains("example.com/mymod/pkg2"))
	assert.False(t, reachable.Contains("fmt"))
	assert.False(t, reachable.Contains("strings"))
}

func TestReachablePackagesExcludesBuildTagPkgs(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	// Main package imports pkg1; pkg2 is behind a build tag and unreachable from the entry point.
	os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module example.com/mymod\n\ngo 1.21\n"), 0644)
	os.MkdirAll(filepath.Join(tmpDir, "cmd", "app"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "cmd", "app", "main.go"), []byte("package main\n"), 0644)

	mock := newMockRunnerForReachable(
		"fmt\nexample.com/mymod/pkg1\nstrings\n")

	reachable, err := ReachablePackages(tmpDir, mock)
	require.NoError(t, err)

	assert.True(t, reachable.Contains("example.com/mymod/pkg1"))
	// pkg2 is NOT reachable because it's not in the deps of main
	assert.False(t, reachable.Contains("example.com/mymod/pkg2"))
}

func TestReachablePackagesFallsBackForLibrary(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	// No main packages found — falls back to ./...
	os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module example.com/mymod\n\ngo 1.21\n"), 0644)
	os.MkdirAll(filepath.Join(tmpDir, "pkg", "lib"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "pkg", "lib", "lib.go"), []byte("package lib\n"), 0644)

	mock := newMockRunnerForReachable(
		"fmt\nexample.com/mymod/pkg1\nexample.com/mymod/pkg2\nstrings\n")

	reachable, err := ReachablePackages(tmpDir, mock)
	require.NoError(t, err)

	assert.True(t, reachable.Contains("example.com/mymod/pkg1"))
	assert.True(t, reachable.Contains("example.com/mymod/pkg2"))
}

func TestReachablePackagesModuleFailure(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	// No go.mod — ReadModulePath returns ""
	mock := newMockRunnerForReachable("")

	reachable, err := ReachablePackages(tmpDir, mock)
	// An empty module prefix reports an empty set, which filters nothing.
	assert.True(t, reachable.IsEmpty())
	assert.Nil(t, err)
}

// newMockRunnerForReachable needs only depsOutput; module path and main packages come from the filesystem.
func newMockRunnerForReachable(depsOutput string) *mockReachableRunner {
	return &mockReachableRunner{depsOutput: depsOutput}
}

type mockReachableRunner struct {
	depsOutput string
}

func (m *mockReachableRunner) Run(cfg runner.Config) (runner.IProcess, error) {
	key := cfg.Name + " " + joinArgs(cfg.Args)
	switch {
	case strings.HasPrefix(key, "go list -deps -f {{.ImportPath}}"):
		return runner.MockProcess([]byte(m.depsOutput), nil), nil
	default:
		return runner.MockProcess(nil, nil), nil
	}
}

func joinArgs(args []string) string {
	result := ""
	for i, a := range args {
		if i > 0 {
			result += " "
		}
		result += a
	}
	return result
}
