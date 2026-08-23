package cmd

// Regression test for the release no-op bug: released "linux" go-toolchain
// binaries are GOOS=cosmo fat-APE slot copies (cosmo matches the `unix` build
// tag but NOT `linux`), so guard code gated linux-only — by a `_linux.go`
// filename suffix or a bare `//go:build linux` — is silently compiled out of
// every shipped binary while the GOOS=linux unit tests stay green. This test
// pins the build constraints themselves: the real classifier must be selected
// for BOTH GOOS=linux and GOOS=cosmo, and the no-op stub for NEITHER.

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

// claudeGuardTagSets are the release-relevant build contexts: the from-source
// GOOS=linux build (CI `build` job, dev machines), the GOOS=cosmo fat APE
// that every published "linux"/"windows" slot actually is, and native darwin
// -- which has no /proc and so gets its own real classifier rather than
// sharing linux/cosmo's.
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

// claudeGuardSourceFiles returns the non-test claudeguard*.go files in this
// package directory.
func claudeGuardSourceFiles(t *testing.T) []string {
	t.Helper()
	matches, err := filepath.Glob("claudeguard*.go")
	require.NoError(t, err)
	var files []string
	for _, m := range matches {
		if strings.HasSuffix(m, "_test.go") {
			continue
		}
		files = append(files, m)
	}
	require.NotEmpty(t, files, "no claudeguard*.go source files found")
	return files
}

// TestClaudeGuardClassifierBuildsForEachPlatform locates every claudeguard*.go
// file defining inspectFD -- the real classifier -- by content rather than by
// filename, so a rename cannot dodge the assertion. darwin and linux/cosmo
// each get their own classifier file (darwin has no /proc), so this checks
// per platform that EXACTLY ONE of them is selected -- neither zero (the
// guard silently no-ops) nor two (an ambiguous build).
func TestClaudeGuardClassifierBuildsForEachPlatform(t *testing.T) {
	var classifiers []string
	for _, f := range claudeGuardSourceFiles(t) {
		data, err := os.ReadFile(f)
		require.NoError(t, err)
		if strings.Contains(string(data), "func inspectFD(") {
			classifiers = append(classifiers, f)
		}
	}
	require.NotEmpty(t, classifiers, "no claudeguard*.go file defines inspectFD")

	for goos, tags := range claudeGuardTagSets {
		var selected []string
		for _, f := range classifiers {
			if fg := filenameGOOS(f); fg != "" && fg != goos {
				continue // filename suffix ties it to a different GOOS
			}
			if line := buildTagLine(t, f); line != "" && !evalTagLine(t, line, tags) {
				continue // //go:build line excludes this platform
			}
			selected = append(selected, f)
		}
		assert.Len(t, selected, 1,
			"GOOS=%s must select exactly one real classifier defining inspectFD, got %v — released binaries would either lack the guard or fail to build ambiguously",
			goos, selected)
	}
}

// claudeGuardDefiners returns the claudeguard*.go files defining decl.
func claudeGuardDefiners(t *testing.T, decl string) []string {
	t.Helper()
	var out []string
	for _, f := range claudeGuardSourceFiles(t) {
		data, err := os.ReadFile(f)
		require.NoError(t, err)
		if strings.Contains(string(data), decl) {
			out = append(out, f)
		}
	}
	require.NotEmpty(t, out, "no claudeguard*.go file defines %q", decl)
	return out
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

// The same class of bug as inspectFD's, one layer down. A cosmo APE runs on
// Linux AND macOS, so it decides its classifier at RUNTIME through
// hostSpecificInspect; a GOOS=linux build answers "never". Exactly one
// definition must be selected per platform: zero fails the build, two is
// ambiguous, and the wrong one silently sends a Mac down the /proc path that
// cannot work there.
func TestClaudeGuardHostDispatchBuildsForEachPlatform(t *testing.T) {
	files := claudeGuardDefiners(t, "func hostSpecificInspect(")
	for goos, tags := range claudeGuardTagSets {
		selected := claudeGuardSelected(t, files, goos, tags)
		if goos == "darwin" {
			assert.Empty(t, selected,
				"GOOS=darwin has its own classifier and must not also select a host dispatch, got %v", selected)
			continue
		}
		assert.Len(t, selected, 1,
			"GOOS=%s must select exactly one hostSpecificInspect, got %v", goos, selected)
	}
}

// The darwin-host classifier is SHARED by the native darwin build and the
// cosmo APE that runs on a Mac. Pin that: if it ever stops being selected for
// cosmo, the APE loses the only classifier that works on macOS while the
// darwin unit tests stay green -- exactly the shape of the original bug.
func TestClaudeGuardDarwinHostClassifierShared(t *testing.T) {
	for _, decl := range []string{
		"func inspectFDDarwinHost(",
		"func fdFileTypeOnDarwinHost(",
		"func socketPeerOnDarwinHost(",
		"func fifoPeerOnDarwinHost(",
		"func isTerminalOnDarwinHost(",
		"func fdPathOnDarwinHost(",
	} {
		t.Run(decl, func(t *testing.T) {
			files := claudeGuardDefiners(t, decl)
			for _, goos := range []string{"darwin", "cosmo"} {
				selected := claudeGuardSelected(t, files, goos, claudeGuardTagSets[goos])
				assert.Len(t, selected, 1,
					"GOOS=%s must select exactly one %s, got %v", goos, decl, selected)
			}
			assert.Empty(t, claudeGuardSelected(t, files, "linux", claudeGuardTagSets["linux"]),
				"%s must not be selected for GOOS=linux, which uses the /proc classifier", decl)
		})
	}
}

func TestClaudeGuardStubExcludedForRealClassifierPlatforms(t *testing.T) {
	const stub = "claudeguard_other.go"
	require.FileExists(t, stub)
	line := buildTagLine(t, stub)
	require.NotEmpty(t, line, "%s must carry a //go:build line", stub)
	for goos, tags := range claudeGuardTagSets {
		assert.False(t, evalTagLine(t, line, tags),
			"%s (the no-op stub) must NOT be selected for GOOS=%s, or it shadows the real classifier", stub, goos)
	}
}
