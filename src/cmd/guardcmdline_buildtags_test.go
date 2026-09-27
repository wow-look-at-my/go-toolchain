package cmd

// The bug this pins: a `_darwin.go` readCmdline beside a `!darwin` /proc
// reader, which GOOS=cosmo resolves to the /proc side.

import (
	"go/build/constraint"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-containers/set"
)

// claudeGuardTagSets are the release-relevant build contexts: from-source GOOS=linux, the
// GOOS=cosmo fat APE every published "linux"/"windows" slot actually is, and native darwin,
// which has no /proc and gets its own classifier.
var claudeGuardTagSets = map[string]map[string]bool{
	"linux":  {"linux": true, "unix": true, "amd64": true},
	"cosmo":  {"cosmo": true, "linux": true, "unix": true, "amd64": true},
	"darwin": {"darwin": true, "unix": true, "amd64": true},
}

// knownGOOSSuffix lists GOOS values whose `_<goos>.go` filename suffix
// imposes an implicit build constraint (upstream GOOS list plus the
// gosmopolitan fork's cosmo).
var knownGOOSSuffix = set.Of(
	"aix", "android", "cosmo", "darwin",
	"dragonfly", "freebsd", "hurd", "illumos",
	"ios", "js", "linux", "netbsd", "openbsd",
	"plan9", "solaris", "wasip1", "windows", "zos",
)

// knownGOARCHSuffix lists GOARCH values recognized in filename suffixes.
var knownGOARCHSuffix = set.Of(
	"386", "amd64", "arm", "arm64", "loong64",
	"mips", "mips64", "mips64le", "mipsle",
	"ppc64", "ppc64le", "riscv64", "s390x", "wasm",
)

// filenameGOOS returns the GOOS a file's `_<goos>[_<goarch>].go` suffix
// implies, or "" when the name imposes no GOOS constraint. Mirrors go/build's
// goodOSArchFile.
func filenameGOOS(name string) string {
	name = strings.TrimSuffix(filepath.Base(name), ".go")
	parts := strings.Split(name, "_")
	if n := len(parts); n >= 2 && knownGOARCHSuffix.Contains(parts[n-1]) {
		parts = parts[:n-1]
	}
	if n := len(parts); n >= 2 && knownGOOSSuffix.Contains(parts[n-1]) {
		return parts[n-1]
	}
	return ""
}

// buildTagLine returns path's //go:build line, or "" when it has none.
func buildTagLine(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if constraint.IsGoBuild(line) {
			return line
		}
	}
	return ""
}

// evalTagLine reports whether the //go:build expression in line is satisfied
// by the given tag set.
func evalTagLine(t *testing.T, line string, tags map[string]bool) bool {
	t.Helper()
	expr, err := constraint.Parse(line)
	require.NoError(t, err, "parsing %q", line)
	return expr.Eval(func(tag string) bool { return tags[tag] })
}

// claudeGuardSelected returns the subset of files selected for a GOOS.
func claudeGuardSelected(t *testing.T, files []string, goos string, tags map[string]bool) []string {
	t.Helper()
	var selected []string
	for _, f := range files {
		if fg := filenameGOOS(f); fg != "" && fg != goos {
			continue
		}
		if line := buildTagLine(t, f); line != "" && !evalTagLine(t, line, tags) {
			continue
		}
		selected = append(selected, f)
	}
	return selected
}

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

// A single definition per platform: none and the guard has no argv to read,
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
	files := guardCmdlineDefiners(t, "func readCmdlinePS(")
	for _, goos := range []string{"darwin", "cosmo"} {
		selected := claudeGuardSelected(t, files, goos, claudeGuardTagSets[goos])
		assert.Len(t, selected, 1,
			"GOOS=%s must select exactly one readCmdlinePS, got %v", goos, selected)
	}
	assert.Empty(t, claudeGuardSelected(t, files, "linux", claudeGuardTagSets["linux"]),
		"readCmdlinePS must not be selected for GOOS=linux, which reads /proc")
}

// The /proc reader is linked into the APE for its linux host, alongside the ps
// reader it picks between at run time.
func TestGuardCmdlineProcReaderBuildsEverywhere(t *testing.T) {
	t.Serial()
	files := guardCmdlineDefiners(t, "func readCmdlineProc(")
	for goos, tags := range claudeGuardTagSets {
		selected := claudeGuardSelected(t, files, goos, tags)
		assert.Len(t, selected, 1,
			"GOOS=%s must select exactly one readCmdlineProc, got %v", goos, selected)
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
