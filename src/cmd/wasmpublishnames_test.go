package cmd

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/build"
)

// TestWasmArtifactNamesInBuildhostPublishSet pins the wasm publishing naming
// contract. The buildhost-publish action selects its upload set by filename:
// regular files (symlinks and checksums.txt are skipped) whose name, after
// stripping a trailing .exe, matches ^(.+)_([a-z]+)_([a-z0-9]+)$, parsed as
// <binary>_{os}_{arch} from the trailing two tokens (filter transcribed from
// the go-font-renderer run 29396682812 logs). Default wasm names use
// buildhost's wasm artifact scheme (wow-look-at-my/buildhost#166): os=wasm
// with arch=js/wasip1, i.e. <name>_wasm_js — they MUST match the pattern and
// parse as os=wasm. The GO_TOOLCHAIN_WASM_PUBLISH=0 opt-out shape
// (<name>_<goos>_wasm.wasm) must NOT match, keeping those artifacts out of
// the publish set entirely (for servers predating #166, where an os=wasm
// upload 400s and one rejected artifact aborts the whole publish).
func TestWasmArtifactNamesInBuildhostPublishSet(t *testing.T) {
	// The exact filter from the buildhost-publish action (wow-look-at-my
	// actions), transcribed from the go-font-renderer failure run's logs.
	publishRe := regexp.MustCompile(`^(.+)_([a-z]+)_([a-z0-9]+)$`)
	parse := func(name string) (osToken, archToken string, ok bool) {
		m := publishRe.FindStringSubmatch(strings.TrimSuffix(name, ".exe"))
		if m == nil {
			return "", "", false
		}
		return m[2], m[3], true
	}

	// Default wasm names are in the publish set and parse as os=wasm with
	// arch carrying the GOOS.
	for goos, wantName := range map[string]string{
		"js":     "mytool_wasm_js",
		"wasip1": "mytool_wasm_wasip1",
	} {
		name := build.BinaryName("mytool", goos, "wasm")
		require.Equal(t, wantName, name)
		osToken, archToken, ok := parse(name)
		require.True(t, ok, "%s must match the publish pattern", name)
		assert.Equal(t, "wasm", osToken, "%s must parse as os=wasm", name)
		assert.Equal(t, goos, archToken, "%s must parse arch=%s", name, goos)
	}
	// Hyphenated binary names keep parsing correctly (the pattern takes the
	// trailing two tokens).
	osToken, archToken, ok := parse(build.BinaryName("my-tool", "js", "wasm"))
	require.True(t, ok)
	assert.Equal(t, "wasm", osToken)
	assert.Equal(t, "js", archToken)

	// The opt-out shape must never enter the publish set.
	_, _, ok = parse(build.UnpublishableWasmName("mytool", "js"))
	assert.False(t, ok, "opt-out wasm names must not match the publish pattern")
	_, _, ok = parse(build.UnpublishableWasmName("my-tool", "wasip1"))
	assert.False(t, ok, "opt-out wasm names must not match the publish pattern")

	// The shipped js exec harness rides along in build/ and checksums.txt but
	// must stay outside the publish set (its trailing token is "exec.js",
	// which the pattern cannot match).
	_, _, ok = parse("wasm_exec.js")
	assert.False(t, ok, "wasm_exec.js must not match the publish pattern")

	// Native platforms keep matching, .exe and hyphens included.
	for _, name := range []string{
		build.BinaryName("mytool", "linux", "amd64"),
		build.BinaryName("mytool", "windows", "amd64"),
		build.BinaryName("my-tool", "darwin", "arm64"),
		build.BinaryName("mytool", "linux", "arm64"),
	} {
		_, _, ok := parse(name)
		assert.True(t, ok, "%s must stay in the publish set", name)
	}
}
