package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-containers/set"
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
		// The cosmo slot copies are what carries the APE to buildhost.
		build.BinaryName("mytool", "linux", "arm64"),
	} {
		_, _, ok := parse(name)
		assert.True(t, ok, "%s must stay in the publish set", name)
	}
}

func TestCopyCosmoSlots(t *testing.T) {
	tmpDir := t.TempDir()
	targets := []build.Target{{ImportPath: "./cmd/mytool", OutputName: "mytool"}}

	fatPath := filepath.Join(tmpDir, "mytool_cosmo_fat")
	require.NoError(t, os.WriteFile(fatPath, []byte("FAT-APE-BYTES"), 0755))

	slots, err := parseCosmoSlots(DefaultCosmoSlots)
	require.NoError(t, err)

	created, replacedFat, err := copyCosmoSlots(targets, tmpDir, slots, set.Set[string]{}, false)
	require.NoError(t, err)

	wantNames := []string{"mytool_linux_amd64", "mytool_linux_arm64", "mytool_windows_amd64.exe"}
	require.Len(t, created, len(wantNames))
	for i, name := range wantNames {
		path := filepath.Join(tmpDir, name)
		assert.Equal(t, path, created[i])

		// Real regular files (never symlinks), byte-identical to the APE,
		// with the executable bit carried over.
		info, err := os.Lstat(path)
		require.NoError(t, err)
		assert.True(t, info.Mode().IsRegular(), "%s must be a regular file, not a symlink", name)
		assert.NotZero(t, info.Mode().Perm()&0111, "%s must be executable", name)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "FAT-APE-BYTES", string(data))
	}

	// The fat name survives locally as a SYMLINK to the first slot copy
	// (buildhost rejects os=cosmo uploads; publish skips symlinks) and is
	// reported as replaced so checksums cover real files only.
	assert.Equal(t, []string{fatPath}, replacedFat)
	info, err := os.Lstat(fatPath)
	require.NoError(t, err)
	assert.Equal(t, os.ModeSymlink, info.Mode()&os.ModeSymlink, "fat name must be a symlink after slot mapping")
	linkTarget, err := os.Readlink(fatPath)
	require.NoError(t, err)
	assert.Equal(t, "mytool_linux_amd64", linkTarget)
	data, err := os.ReadFile(fatPath) // follows the link
	require.NoError(t, err)
	assert.Equal(t, "FAT-APE-BYTES", string(data))
}

func TestCopyCosmoSlotsDropsFatInCI(t *testing.T) {
	tmpDir := t.TempDir()
	targets := []build.Target{{ImportPath: "./cmd/mytool", OutputName: "mytool"}}

	fatPath := filepath.Join(tmpDir, "mytool_cosmo_fat")
	require.NoError(t, os.WriteFile(fatPath, []byte("FAT-APE-BYTES"), 0755))

	slots, err := parseCosmoSlots([]string{"linux/amd64"})
	require.NoError(t, err)

	var created, replacedFat []string
	output := captureStdout(func() {
		created, replacedFat, err = copyCosmoSlots(targets, tmpDir, slots, set.Set[string]{}, true)
	})
	require.NoError(t, err)
	require.Len(t, created, 1)
	assert.Equal(t, []string{fatPath}, replacedFat)

	// dropFat removes the fat name entirely: upload-artifact dereferences
	// symlinks, so only removal keeps a publish-breaking os=cosmo artifact
	// out of the uploaded build/ directory.
	_, err = os.Lstat(fatPath)
	assert.True(t, os.IsNotExist(err), "fat name must be removed in CI")
	assert.Contains(t, output, "DROP mytool_cosmo_fat")
	data, err := os.ReadFile(filepath.Join(tmpDir, "mytool_linux_amd64"))
	require.NoError(t, err)
	assert.Equal(t, "FAT-APE-BYTES", string(data))
}

func TestCopyCosmoSlotsAllSlotsCollidedKeepsFat(t *testing.T) {
	tmpDir := t.TempDir()
	targets := []build.Target{{ImportPath: "./cmd/mytool", OutputName: "mytool"}}

	fatPath := filepath.Join(tmpDir, "mytool_cosmo_fat")
	require.NoError(t, os.WriteFile(fatPath, []byte("FAT"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "mytool_linux_amd64"), []byte("NATIVE"), 0755))

	slots, err := parseCosmoSlots([]string{"linux/amd64"})
	require.NoError(t, err)

	nativeBuilt := set.Of("mytool_linux_amd64")
	var created, replacedFat []string
	output := captureCombinedOutput(func() {
		created, replacedFat, err = copyCosmoSlots(targets, tmpDir, slots, nativeBuilt, false)
	})
	require.NoError(t, err)
	assert.Empty(t, created)
	assert.Empty(t, replacedFat)

	// With no surviving slot copy there is nothing to link to: the real fat
	// file is kept (and cannot be published to buildhost until the server
	// accepts os=cosmo).
	info, err := os.Lstat(fatPath)
	require.NoError(t, err)
	assert.True(t, info.Mode().IsRegular(), "fat must stay a real file when no slot copy exists")
	assert.Contains(t, output, "KEEP mytool_cosmo_fat")
}

func TestCopyCosmoSlotsNativeCollisionWins(t *testing.T) {
	tmpDir := t.TempDir()
	targets := []build.Target{{ImportPath: "./cmd/mytool", OutputName: "mytool"}}

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "mytool_cosmo_fat"), []byte("FAT"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "mytool_linux_amd64"), []byte("NATIVE"), 0755))

	slots, err := parseCosmoSlots([]string{"linux/amd64", "linux/arm64"})
	require.NoError(t, err)

	nativeBuilt := set.Of("mytool_linux_amd64")
	var created []string
	output := captureCombinedOutput(func() {
		created, _, err = copyCosmoSlots(targets, tmpDir, slots, nativeBuilt, false)
	})
	require.NoError(t, err)

	// The collided slot is skipped (native bytes survive) and not reported
	// as created; the other slot is copied.
	require.Len(t, created, 1)
	assert.Equal(t, filepath.Join(tmpDir, "mytool_linux_arm64"), created[0])
	data, err := os.ReadFile(filepath.Join(tmpDir, "mytool_linux_amd64"))
	require.NoError(t, err)
	assert.Equal(t, "NATIVE", string(data))
	assert.Contains(t, output, "SKIP mytool_linux_amd64")

	// The fat symlink must point at the first slot copy actually CREATED
	// (linux/arm64), not the collided first slot.
	linkTarget, err := os.Readlink(filepath.Join(tmpDir, "mytool_cosmo_fat"))
	require.NoError(t, err)
	assert.Equal(t, "mytool_linux_arm64", linkTarget)
}

func TestCopyCosmoSlotsMissingFatAPE(t *testing.T) {
	tmpDir := t.TempDir()
	targets := []build.Target{{ImportPath: "./cmd/mytool", OutputName: "mytool"}}
	slots := []buildPlatform{{OS: "linux", Arch: "amd64"}}

	_, _, err := copyCosmoSlots(targets, tmpDir, slots, set.Set[string]{}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mytool_cosmo_fat")
}

func TestCopyCosmoSlotsReplacesStaleSymlink(t *testing.T) {
	tmpDir := t.TempDir()
	targets := []build.Target{{ImportPath: "./cmd/mytool", OutputName: "mytool"}}

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "mytool_cosmo_fat"), []byte("FAT"), 0755))
	// A stale symlink from a previous run must be replaced by a real file,
	// not written through.
	other := filepath.Join(tmpDir, "other_file")
	require.NoError(t, os.WriteFile(other, []byte("OTHER"), 0644))
	require.NoError(t, os.Symlink(other, filepath.Join(tmpDir, "mytool_linux_amd64")))

	slots := []buildPlatform{{OS: "linux", Arch: "amd64"}}
	_, _, err := copyCosmoSlots(targets, tmpDir, slots, set.Set[string]{}, false)
	require.NoError(t, err)

	info, err := os.Lstat(filepath.Join(tmpDir, "mytool_linux_amd64"))
	require.NoError(t, err)
	assert.True(t, info.Mode().IsRegular())
	data, err := os.ReadFile(other)
	require.NoError(t, err)
	assert.Equal(t, "OTHER", string(data), "symlink target must not be overwritten")
}
