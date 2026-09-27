package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTargetProbe stages a directive that records the GOOS and GOARCH its
// process was given, and returns the go file the directive belongs to.
func writeTargetProbe(t *testing.T, dir string) string {
	t.Helper()
	testFile := filepath.Join(dir, "test.go")
	require.NoError(t, os.WriteFile(testFile, []byte("package main\n"), 0o644))
	probe := "printf '%s %s' \"$GOOS\" \"$GOARCH\" > target.txt\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "probe.sh"), []byte(probe), 0o755))
	return testFile
}

// A directive builds a tool and then runs it on this machine, so the tool is
// an APE for this machine's architecture, whatever target the pipeline
// inherited.
func TestExecuteDirectiveBuildsAnAPE(t *testing.T) {
	// Not parallel: the inherited target is what this test replaces.
	dir := t.TempDir()
	testFile := writeTargetProbe(t, dir)
	t.Setenv("GOOS", "linux")
	t.Setenv("GOARCH", "riscv64")

	d := generateDirective{File: testFile, Line: 1, Command: "sh probe.sh"}
	require.NoError(t, executeDirective(d, true))

	got, err := os.ReadFile(filepath.Join(dir, "target.txt"))
	require.NoError(t, err)
	assert.Equal(t, "cosmo "+runtime.GOARCH, string(got))
}

// A directive that names its own target keeps it: the host values are the
// process environment, which the command's own env prefix overrides.
func TestExecuteDirectiveKeepsItsOwnTarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	testFile := writeTargetProbe(t, dir)

	d := generateDirective{File: testFile, Line: 1, Command: "env GOOS=js GOARCH=wasm sh probe.sh"}
	require.NoError(t, executeDirective(d, true))

	got, err := os.ReadFile(filepath.Join(dir, "target.txt"))
	require.NoError(t, err)
	assert.Equal(t, "js wasm", string(got))
}
