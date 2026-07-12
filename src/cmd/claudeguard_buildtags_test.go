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
)

// claudeGuardTagSets are the two release-relevant build contexts: the
// from-source GOOS=linux build (CI `build` job, dev machines) and the
// GOOS=cosmo fat APE that every published "linux"/"windows" slot actually is.
var claudeGuardTagSets = map[string]map[string]bool{
	"linux": {"linux": true, "unix": true, "amd64": true},
	"cosmo": {"cosmo": true, "unix": true, "amd64": true},
}

// knownGOOSSuffix lists GOOS values whose `_<goos>.go` filename suffix
// imposes an implicit build constraint (upstream GOOS list plus the
// gosmopolitan fork's cosmo).
var knownGOOSSuffix = map[string]bool{
	"aix": true, "android": true, "cosmo": true, "darwin": true,
	"dragonfly": true, "freebsd": true, "hurd": true, "illumos": true,
	"ios": true, "js": true, "linux": true, "netbsd": true, "openbsd": true,
	"plan9": true, "solaris": true, "wasip1": true, "windows": true, "zos": true,
}

// knownGOARCHSuffix lists GOARCH values recognized in filename suffixes.
var knownGOARCHSuffix = map[string]bool{
	"386": true, "amd64": true, "arm": true, "arm64": true, "loong64": true,
	"mips": true, "mips64": true, "mips64le": true, "mipsle": true,
	"ppc64": true, "ppc64le": true, "riscv64": true, "s390x": true, "wasm": true,
}

// filenameGOOS returns the GOOS a file's `_<goos>[_<goarch>].go` suffix
// implies, or "" when the name imposes no GOOS constraint. Mirrors go/build's
// goodOSArchFile.
func filenameGOOS(name string) string {
	name = strings.TrimSuffix(filepath.Base(name), ".go")
	parts := strings.Split(name, "_")
	if n := len(parts); n >= 2 && knownGOARCHSuffix[parts[n-1]] {
		parts = parts[:n-1]
	}
	if n := len(parts); n >= 2 && knownGOOSSuffix[parts[n-1]] {
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

func TestClaudeGuardClassifierBuildsForLinuxAndCosmo(t *testing.T) {
	// Locate the real classifier by content — the file defining inspectFD —
	// rather than by filename, so a rename cannot dodge the assertion.
	var classifier string
	for _, f := range claudeGuardSourceFiles(t) {
		data, err := os.ReadFile(f)
		require.NoError(t, err)
		if strings.Contains(string(data), "func inspectFD(") {
			require.Empty(t, classifier, "inspectFD defined in both %s and %s", classifier, f)
			classifier = f
		}
	}
	require.NotEmpty(t, classifier, "no claudeguard*.go file defines inspectFD")

	line := buildTagLine(t, classifier)
	for goos, tags := range claudeGuardTagSets {
		if fg := filenameGOOS(classifier); fg != "" {
			assert.Equal(t, goos, fg,
				"classifier %s has a filename GOOS suffix that excludes it from GOOS=%s builds — released binaries are GOOS=cosmo APEs, so the guard would be a no-op in every release",
				classifier, goos)
		}
		if line != "" {
			assert.True(t, evalTagLine(t, line, tags),
				"classifier %s constraint %q must be satisfied for GOOS=%s — released binaries are GOOS=cosmo APEs, so the guard would be a no-op in every release",
				classifier, line, goos)
		}
	}
}

func TestClaudeGuardStubExcludedForLinuxAndCosmo(t *testing.T) {
	const stub = "claudeguard_other.go"
	require.FileExists(t, stub)
	line := buildTagLine(t, stub)
	require.NotEmpty(t, line, "%s must carry a //go:build line", stub)
	for goos, tags := range claudeGuardTagSets {
		assert.False(t, evalTagLine(t, line, tags),
			"%s (the no-op stub) must NOT be selected for GOOS=%s, or it shadows the real classifier", stub, goos)
	}
}
