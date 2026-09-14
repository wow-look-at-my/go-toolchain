package cmd

// The bug this pins: readCmdline lived in a `_darwin.go` file next to a
// `!darwin` /proc reader. GOOS=cosmo excludes the first and selects the
// second, so the published APE asked a Mac for /proc, read nothing, and
// acquitted every captured run the guard exists to refuse -- while the
// GOOS=darwin unit tests, which do select the sysctl reader, stayed green.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// guardCmdlineSourceFiles returns the non-test guardcmdline*.go files.
func guardCmdlineSourceFiles(t *testing.T) []string {
	t.Helper()
	matches, err := filepath.Glob("guardcmdline*.go")
	require.NoError(t, err)
	var files []string
	for _, m := range matches {
		if !strings.HasSuffix(m, "_test.go") {
			files = append(files, m)
		}
	}
	require.NotEmpty(t, files, "no guardcmdline*.go source files found")
	return files
}

// guardCmdlineDefiners returns the guardcmdline*.go files defining decl.
func guardCmdlineDefiners(t *testing.T, decl string) []string {
	t.Helper()
	var out []string
	for _, f := range guardCmdlineSourceFiles(t) {
		data, err := os.ReadFile(f)
		require.NoError(t, err)
		if strings.Contains(string(data), decl) {
			out = append(out, f)
		}
	}
	require.NotEmpty(t, out, "no guardcmdline*.go file defines %q", decl)
	return out
}

// Exactly one definition per platform: none and the guard has no argv to read,
// several and the build is ambiguous.
func TestGuardCmdlineReaderBuildsForEachPlatform(t *testing.T) {
	t.Serial()
	files := guardCmdlineDefiners(t, "func readCmdline(")
	for goos, tags := range claudeGuardTagSets {
		selected := claudeGuardSelected(t, files, goos, tags)
		assert.Len(t, selected, 1,
			"GOOS=%s must select exactly one readCmdline, got %v", goos, selected)
	}
}

// The ps reader is what the APE has on a Mac, so it must be selected for
// cosmo. A GOOS=linux build never needs it and must not carry it.
func TestGuardCmdlinePSReaderSharedWithCosmo(t *testing.T) {
	t.Serial()
	files := guardCmdlineDefiners(t, "func psCmdline(")
	for _, goos := range []string{"darwin", "cosmo"} {
		selected := claudeGuardSelected(t, files, goos, claudeGuardTagSets[goos])
		assert.Len(t, selected, 1,
			"GOOS=%s must select exactly one psCmdline, got %v", goos, selected)
	}
	assert.Empty(t, claudeGuardSelected(t, files, "linux", claudeGuardTagSets["linux"]),
		"psCmdline must not be selected for GOOS=linux, which reads /proc")
}

// The /proc reader is linked into the APE for its linux host, alongside the ps
// reader it picks between at run time.
func TestGuardCmdlineProcReaderBuildsEverywhere(t *testing.T) {
	t.Serial()
	files := guardCmdlineDefiners(t, "func procCmdline(")
	for goos, tags := range claudeGuardTagSets {
		selected := claudeGuardSelected(t, files, goos, tags)
		assert.Len(t, selected, 1,
			"GOOS=%s must select exactly one procCmdline, got %v", goos, selected)
	}
}

// The ancestry walk starts at this process, so the pid it starts from has to
// exist in every build: a platform split there is the same silent no-op the
// reader split above was.
func TestGuardCmdlineSelfPIDBuildsEverywhere(t *testing.T) {
	t.Serial()
	files := guardCmdlineDefiners(t, "func selfPID(")
	for goos, tags := range claudeGuardTagSets {
		selected := claudeGuardSelected(t, files, goos, tags)
		assert.Len(t, selected, 1,
			"GOOS=%s must select exactly one selfPID, got %v", goos, selected)
	}
}
