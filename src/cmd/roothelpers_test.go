package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNeedsGenerateNoDirectives(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	os.WriteFile(dir+"/main.go", []byte("package main\nfunc main() {}\n"), 0644)

	t.Chdir(dir)

	assert.False(t, needsGenerate())
}

func TestNeedsGenerateWithDirective(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	os.WriteFile(dir+"/main.go", []byte("package main\n//go:generate echo hello\nfunc main() {}\n"), 0644)

	t.Chdir(dir)

	assert.True(t, needsGenerate())
}

func TestFindGoModules_CurrentDir(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\ngo 1.21\n"), 0644)

	t.Chdir(dir)

	modules := findGoModules()
	require.Equal(t, 1, len(modules))
	assert.Equal(t, ".", modules[0])
}

func TestFindGoModules_Subdirectories(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	// No go.mod in root — create subdirectories with go.mod
	os.MkdirAll(filepath.Join(dir, "svc-a"), 0755)
	os.MkdirAll(filepath.Join(dir, "svc-b"), 0755)
	os.WriteFile(filepath.Join(dir, "svc-a", "go.mod"), []byte("module test/a\ngo 1.21\n"), 0644)
	os.WriteFile(filepath.Join(dir, "svc-b", "go.mod"), []byte("module test/b\ngo 1.21\n"), 0644)

	t.Chdir(dir)

	modules := findGoModules()
	assert.Equal(t, 2, len(modules))
}

// A root go.mod used to END the search, so a nested module never built and
// never ran a test while the run reported green. The root still leads.
func TestFindGoModules_RootDoesNotHideNested(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\ngo 1.21\n"), 0644)
	os.MkdirAll(filepath.Join(dir, "tool"), 0755)
	os.WriteFile(filepath.Join(dir, "tool", "go.mod"), []byte("module test/tool\ngo 1.21\n"), 0644)
	os.MkdirAll(filepath.Join(dir, "examples", "demo"), 0755)
	os.WriteFile(filepath.Join(dir, "examples", "demo", "go.mod"), []byte("module demo\ngo 1.21\n"), 0644)

	t.Chdir(dir)

	modules := findGoModules()
	require.Equal(t, 3, len(modules), "the root and both nested modules")
	assert.Equal(t, ".", modules[0], "the root module leads")
	assert.Contains(t, modules, filepath.Join("examples", "demo"))
	assert.Contains(t, modules, "tool")
}

// A go.mod under testdata is a fixture for somebody's test. go ignores
// testdata and so does this walk, or every table-driven module fixture in
// the tree becomes a build target.
func TestFindGoModules_SkipsTestdata(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\ngo 1.21\n"), 0644)
	os.MkdirAll(filepath.Join(dir, "testdata", "broken"), 0755)
	os.WriteFile(filepath.Join(dir, "testdata", "broken", "go.mod"), []byte("module broken\n"), 0644)

	t.Chdir(dir)

	modules := findGoModules()
	require.Equal(t, 1, len(modules))
	assert.Equal(t, ".", modules[0])
}

func TestFindGoModules_SkipsHiddenAndVendor(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	// No go.mod in root
	os.MkdirAll(filepath.Join(dir, ".hidden"), 0755)
	os.MkdirAll(filepath.Join(dir, "vendor"), 0755)
	os.MkdirAll(filepath.Join(dir, "node_modules"), 0755)
	os.MkdirAll(filepath.Join(dir, "real"), 0755)
	os.WriteFile(filepath.Join(dir, ".hidden", "go.mod"), []byte("module hidden\n"), 0644)
	os.WriteFile(filepath.Join(dir, "vendor", "go.mod"), []byte("module vendor\n"), 0644)
	os.WriteFile(filepath.Join(dir, "node_modules", "go.mod"), []byte("module nm\n"), 0644)
	os.WriteFile(filepath.Join(dir, "real", "go.mod"), []byte("module real\n"), 0644)

	t.Chdir(dir)

	modules := findGoModules()
	require.Equal(t, 1, len(modules))
	assert.Equal(t, "real", modules[0])
}

func TestFindGoModules_NoModules(t *testing.T) {
	t.Serial()
	dir := t.TempDir()

	t.Chdir(dir)

	modules := findGoModules()
	assert.Equal(t, 0, len(modules))
}

// Subcommands of skip-listed commands (e.g. `version raw`) must inherit the
// cache skip — cobra passes the leaf command to PersistentPreRunE, so the
// skip check has to walk ancestors. Regression test for the release-job
// "Determine tag" failure from `./build/go-toolchain version raw`.
// version is exempt from the agent output guard too: it prints build metadata
// and no build result, and this repository's own dats suite runs it -- dats
// captures stdout to assert on it, so a guarded version fails the integration
// phase of every run under an agent.
func TestSkipCache_VersionSubcommandsSkip(t *testing.T) {
	t.Serial()
	t.Setenv("CI", "true")

	for _, argv := range [][]string{
		{"version"},
		{"version", "raw"},
		{"version", "json"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			leaf, _, err := rootCmd.Find(argv)
			require.NoError(t, err)
			require.NotNil(t, leaf)
			assert.True(t, skipUpToDateCheck(leaf),
				"skipUpToDateCheck should return true for %q (Name=%q)", argv, leaf.Name())
			assert.True(t, skipAgentGuard(leaf),
				"skipAgentGuard should be true for %q -- it prints no build result", argv)
			// End-to-end: PersistentPreRunE must not fail for this leaf.
			assert.NoError(t, rootCmd.PersistentPreRunE(leaf, nil))
		})
	}
}

// Lock in that subcommands NOT under a skip-listed parent still trigger the
// up-to-date fast exit — so the ancestor walk in skipUpToDateCheck doesn't
// accidentally match too broadly.
func TestSkipCache_NonSkippedSubcommandsStillRun(t *testing.T) {
	t.Serial()
	for _, argv := range [][]string{
		{"bench", "run"},
		{"unignore", "coverage"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			leaf, _, err := rootCmd.Find(argv)
			require.NoError(t, err)
			require.NotNil(t, leaf)
			assert.False(t, skipUpToDateCheck(leaf),
				"skipUpToDateCheck should remain false for %q", argv)
		})
	}
}
