package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/build"
)

func TestParseTargetList(t *testing.T) {
	tests := []struct {
		name    string
		entries []string
		want    []buildPlatform
		wantErr string
	}{
		{
			name:    "single pair",
			entries: []string{"linux/amd64"},
			want:    []buildPlatform{{OS: "linux", Arch: "amd64"}},
		},
		{
			name:    "multiple pairs",
			entries: []string{"darwin/amd64", "windows/arm64"},
			want:    []buildPlatform{{OS: "darwin", Arch: "amd64"}, {OS: "windows", Arch: "arm64"}},
		},
		{
			name:    "cosmo alone",
			entries: []string{"cosmo"},
			want:    []buildPlatform{{OS: "cosmo", Arch: "fat"}},
		},
		{
			name:    "cosmo mixed with native pairs",
			entries: []string{"cosmo", "darwin/amd64", "windows/arm64"},
			want: []buildPlatform{
				{OS: "cosmo", Arch: "fat"},
				{OS: "darwin", Arch: "amd64"},
				{OS: "windows", Arch: "arm64"},
			},
		},
		{
			name:    "whitespace trimmed",
			entries: []string{" linux/amd64 "},
			want:    []buildPlatform{{OS: "linux", Arch: "amd64"}},
		},
		{
			name:    "empty list",
			entries: nil,
			wantErr: "at least one entry",
		},
		{
			name:    "empty entry",
			entries: []string{"linux/amd64", ""},
			wantErr: "empty entry",
		},
		{
			name:    "missing slash",
			entries: []string{"linux"},
			wantErr: "os/arch pairs",
		},
		{
			name:    "unknown os lists valid values",
			entries: []string{"linsux/amd64"},
			wantErr: "unknown OS \"linsux\"",
		},
		{
			name:    "unknown arch lists valid values",
			entries: []string{"linux/amd65"},
			wantErr: "unknown architecture \"amd65\"",
		},
		{
			name:    "cosmo with arch is rejected",
			entries: []string{"cosmo/amd64"},
			wantErr: "always one fat APE",
		},
		{
			name:    "wasm targets",
			entries: []string{"js/wasm", "wasip1/wasm"},
			want: []buildPlatform{
				{OS: "js", Arch: "wasm"},
				{OS: "wasip1", Arch: "wasm"},
			},
		},
		{
			name:    "wasm mixed with native and cosmo",
			entries: []string{"cosmo", "linux/amd64", "js/wasm"},
			want: []buildPlatform{
				{OS: "cosmo", Arch: "fat"},
				{OS: "linux", Arch: "amd64"},
				{OS: "js", Arch: "wasm"},
			},
		},
		{
			name:    "js without wasm arch is rejected",
			entries: []string{"js/amd64"},
			wantErr: "only builds WebAssembly",
		},
		{
			name:    "wasip1 without wasm arch is rejected",
			entries: []string{"wasip1/arm64"},
			wantErr: "only builds WebAssembly",
		},
		{
			name:    "wasm arch without wasm os is rejected",
			entries: []string{"linux/wasm"},
			wantErr: "needs GOOS js or wasip1",
		},
		{
			name:    "duplicate pair",
			entries: []string{"linux/amd64", "linux/amd64"},
			wantErr: "duplicate target",
		},
		{
			name:    "duplicate cosmo",
			entries: []string{"cosmo", "cosmo"},
			wantErr: "duplicate target",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTargetList(tt.entries)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseTargetListErrorNamesValidValues(t *testing.T) {
	_, err := parseTargetList([]string{"beos/amd64"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "linux")
	assert.Contains(t, err.Error(), "windows")

	_, err = parseTargetList([]string{"linux/pentium"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amd64")
	assert.Contains(t, err.Error(), "arm64")
}

func TestParseCosmoSlots(t *testing.T) {
	tests := []struct {
		name    string
		entries []string
		want    []buildPlatform
		wantErr string
	}{
		{
			name:    "defaults",
			entries: DefaultCosmoSlots,
			// darwin/arm64 is intentionally NOT a default slot: the full
			// pipeline wedges under the APE on macOS (see DefaultCosmoSlots),
			// so macs get a native binary by default until that is fixed.
			want: []buildPlatform{
				{OS: "linux", Arch: "amd64"},
				{OS: "linux", Arch: "arm64"},
				{OS: "windows", Arch: "amd64"},
			},
		},
		{
			name:    "none disables mapping",
			entries: []string{"none"},
			want:    nil,
		},
		{
			name:    "none mixed with slots is rejected",
			entries: []string{"none", "linux/amd64"},
			wantErr: "must be the only value",
		},
		{
			name:    "cosmo is not a slot",
			entries: []string{"cosmo/fat"},
			wantErr: "not a slot",
		},
		{
			name:    "wasm is not a slot",
			entries: []string{"js/wasm"},
			wantErr: "not a wasm binary",
		},
		{
			name:    "unknown arch",
			entries: []string{"linux/amd65"},
			wantErr: "unknown architecture",
		},
		{
			name:    "duplicate slot",
			entries: []string{"linux/amd64", "linux/amd64"},
			wantErr: "duplicate --cosmo-slots",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCosmoSlots(tt.entries)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveMatrixPlatformsCartesian(t *testing.T) {
	oldOS, oldArch, oldTargets := matrixOS, matrixArch, matrixTargets
	defer func() { matrixOS, matrixArch, matrixTargets = oldOS, oldArch, oldTargets }()

	matrixOS = []string{"linux", "darwin"}
	matrixArch = []string{"amd64", "arm64"}
	matrixTargets = nil

	got, err := resolveMatrixPlatforms()
	require.NoError(t, err)
	assert.Equal(t, []buildPlatform{
		{OS: "linux", Arch: "amd64"},
		{OS: "linux", Arch: "arm64"},
		{OS: "darwin", Arch: "amd64"},
		{OS: "darwin", Arch: "arm64"},
	}, got)
}

func TestResolveMatrixPlatformsTargetsReplaceCartesian(t *testing.T) {
	oldOS, oldArch, oldTargets := matrixOS, matrixArch, matrixTargets
	defer func() { matrixOS, matrixArch, matrixTargets = oldOS, oldArch, oldTargets }()

	matrixOS = DefaultOS
	matrixArch = DefaultArch
	matrixTargets = []string{"cosmo", "windows/arm64"}

	got, err := resolveMatrixPlatforms()
	require.NoError(t, err)
	assert.Equal(t, []buildPlatform{
		{OS: "cosmo", Arch: "fat"},
		{OS: "windows", Arch: "arm64"},
	}, got)
}

func TestResolveMatrixPlatformsRejectsCosmoOS(t *testing.T) {
	oldOS, oldArch, oldTargets := matrixOS, matrixArch, matrixTargets
	defer func() { matrixOS, matrixArch, matrixTargets = oldOS, oldArch, oldTargets }()

	matrixOS = []string{"cosmo"}
	matrixArch = []string{"amd64"}
	matrixTargets = nil

	_, err := resolveMatrixPlatforms()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--targets cosmo")
}

func TestResolveMatrixPlatformsRejectsWasmInCartesian(t *testing.T) {
	oldOS, oldArch, oldTargets := matrixOS, matrixArch, matrixTargets
	defer func() { matrixOS, matrixArch, matrixTargets = oldOS, oldArch, oldTargets }()
	matrixTargets = nil

	// GOOS js/wasip1 through --os point at --targets.
	matrixOS, matrixArch = []string{"js"}, []string{"amd64"}
	_, err := resolveMatrixPlatforms()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--targets js/wasm")

	matrixOS, matrixArch = []string{"wasip1"}, []string{"amd64"}
	_, err = resolveMatrixPlatforms()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--targets wasip1/wasm")

	// GOARCH wasm through --arch points at --targets too.
	matrixOS, matrixArch = []string{"linux"}, []string{"wasm"}
	_, err = resolveMatrixPlatforms()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--targets js/wasm")
}

func TestBuildPlatformPredicates(t *testing.T) {
	cosmo := buildPlatform{OS: "cosmo", Arch: "fat"}
	js := buildPlatform{OS: "js", Arch: "wasm"}
	wasip1 := buildPlatform{OS: "wasip1", Arch: "wasm"}
	linux := buildPlatform{OS: "linux", Arch: "amd64"}

	assert.True(t, cosmo.IsCosmo())
	assert.False(t, cosmo.IsWasm())
	assert.True(t, js.IsWasm())
	assert.True(t, wasip1.IsWasm())
	assert.False(t, linux.IsWasm())

	// The fork toolchain builds cosmo and wasm; everything else uses the go
	// on PATH.
	assert.True(t, cosmo.NeedsForkToolchain())
	assert.True(t, js.NeedsForkToolchain())
	assert.True(t, wasip1.NeedsForkToolchain())
	assert.False(t, linux.NeedsForkToolchain())
}

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

	created, replacedFat, err := copyCosmoSlots(targets, tmpDir, slots, nil, false)
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
		created, replacedFat, err = copyCosmoSlots(targets, tmpDir, slots, nil, true)
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

	nativeBuilt := map[string]bool{"mytool_linux_amd64": true}
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

	nativeBuilt := map[string]bool{"mytool_linux_amd64": true}
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

	_, _, err := copyCosmoSlots(targets, tmpDir, slots, nil, false)
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
	_, _, err := copyCosmoSlots(targets, tmpDir, slots, nil, false)
	require.NoError(t, err)

	info, err := os.Lstat(filepath.Join(tmpDir, "mytool_linux_amd64"))
	require.NoError(t, err)
	assert.True(t, info.Mode().IsRegular())
	data, err := os.ReadFile(other)
	require.NoError(t, err)
	assert.Equal(t, "OTHER", string(data), "symlink target must not be overwritten")
}
