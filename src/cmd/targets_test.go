package cmd

import (
	"os"
	"path/filepath"
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
	output := captureStdout(func() {
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
	output := captureStdout(func() {
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
