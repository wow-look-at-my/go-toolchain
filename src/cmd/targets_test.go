package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			name:    "wasm targets canonical spelling",
			entries: []string{"wasm/js", "wasm/wasip1"},
			want: []buildPlatform{
				{OS: "js", Arch: "wasm"},
				{OS: "wasip1", Arch: "wasm"},
			},
		},
		{
			// The GOOS-order spellings are a quiet compatibility alias
			// (already shipped in released consumers) and normalize to the
			// same internal targets as the canonical wasm/js form.
			name:    "wasm targets GOOS-order alias",
			entries: []string{"js/wasm", "wasip1/wasm"},
			want: []buildPlatform{
				{OS: "js", Arch: "wasm"},
				{OS: "wasip1", Arch: "wasm"},
			},
		},
		{
			// Mixing the canonical spelling and its alias dedupes to ONE
			// target — they are the same platform after normalization.
			name:    "wasm spellings dedupe to one target",
			entries: []string{"wasm/js", "js/wasm", "wasip1/wasm", "wasm/wasip1"},
			want: []buildPlatform{
				{OS: "js", Arch: "wasm"},
				{OS: "wasip1", Arch: "wasm"},
			},
		},
		{
			name:    "identical wasm entry twice is still a duplicate",
			entries: []string{"wasm/js", "wasm/js"},
			wantErr: "duplicate target",
		},
		{
			name:    "wasm with a non-wasm flavor is rejected",
			entries: []string{"wasm/amd64"},
			wantErr: "wasm targets are wasm/js or wasm/wasip1",
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

	// GOOS js/wasip1 through --os point at the os=wasm pairing (and the
	// canonical --targets spelling).
	matrixOS, matrixArch = []string{"js"}, []string{"amd64"}
	_, err := resolveMatrixPlatforms()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--os wasm --arch js")
	assert.Contains(t, err.Error(), "--targets wasm/js")

	matrixOS, matrixArch = []string{"wasip1"}, []string{"amd64"}
	_, err = resolveMatrixPlatforms()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--os wasm --arch wasip1")
	assert.Contains(t, err.Error(), "--targets wasm/wasip1")

	// GOARCH "wasm" is spelled as the OS in the wasm pairing.
	matrixOS, matrixArch = []string{"linux"}, []string{"wasm"}
	_, err = resolveMatrixPlatforms()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--os wasm --arch js")
	assert.Contains(t, err.Error(), "--targets wasm/js")
}

func TestResolveMatrixPlatformsWasmCartesian(t *testing.T) {
	oldOS, oldArch, oldTargets := matrixOS, matrixArch, matrixTargets
	defer func() { matrixOS, matrixArch, matrixTargets = oldOS, oldArch, oldTargets }()
	matrixTargets = nil

	// --os wasm --arch js: same normalized target as --targets wasm/js.
	matrixOS, matrixArch = []string{"wasm"}, []string{"js"}
	got, err := resolveMatrixPlatforms()
	require.NoError(t, err)
	assert.Equal(t, []buildPlatform{{OS: "js", Arch: "wasm"}}, got)
	fromTargets, err := parseTargetList([]string{"wasm/js"})
	require.NoError(t, err)
	assert.Equal(t, fromTargets, got, "--os wasm --arch js must equal --targets wasm/js")

	// Both flavors at once.
	matrixOS, matrixArch = []string{"wasm"}, []string{"js", "wasip1"}
	got, err = resolveMatrixPlatforms()
	require.NoError(t, err)
	assert.Equal(t, []buildPlatform{
		{OS: "js", Arch: "wasm"},
		{OS: "wasip1", Arch: "wasm"},
	}, got)

	// MIXED list: impossible cross combinations (wasm x native arch, native
	// os x wasm flavor) are skipped with one aggregate warning; the possible
	// ones build.
	matrixOS, matrixArch = []string{"linux", "wasm"}, []string{"amd64", "js"}
	var warnOut string
	warnOut = captureCombinedOutput(func() {
		got, err = resolveMatrixPlatforms()
	})
	require.NoError(t, err)
	assert.Equal(t, []buildPlatform{
		{OS: "linux", Arch: "amd64"},
		{OS: "js", Arch: "wasm"},
	}, got)
	assert.Contains(t, warnOut, "linux/js")
	assert.Contains(t, warnOut, "wasm/amd64")

	// os=wasm with no wasm flavor arch anywhere: nothing satisfiable, fail
	// fast with the exact-pairing error.
	matrixOS, matrixArch = []string{"wasm"}, []string{"amd64"}
	_, err = resolveMatrixPlatforms()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no buildable os/arch combinations")
	assert.Contains(t, err.Error(), "--os wasm --arch js")

	// A wasm flavor arch with no os=wasm in the list: error with the fix.
	matrixOS, matrixArch = []string{"linux"}, []string{"js"}
	_, err = resolveMatrixPlatforms()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--os wasm --arch js")
	assert.Contains(t, err.Error(), "--targets wasm/js")

	// Mixed list where the wasm os gets no flavor: the native combos build,
	// the wasm ones are skipped with the warning.
	matrixOS, matrixArch = []string{"linux", "wasm"}, []string{"amd64"}
	warnOut = captureCombinedOutput(func() {
		got, err = resolveMatrixPlatforms()
	})
	require.NoError(t, err)
	assert.Equal(t, []buildPlatform{{OS: "linux", Arch: "amd64"}}, got)
	assert.Contains(t, warnOut, "wasm/amd64")
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
