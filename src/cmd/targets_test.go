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
			name:    "cosmo alone",
			entries: []string{"cosmo"},
			want:    []buildPlatform{{OS: "cosmo", Arch: "fat"}},
		},
		{
			name:    "whitespace trimmed",
			entries: []string{" wasm/js "},
			want:    []buildPlatform{{OS: "js", Arch: "wasm"}},
		},
		{
			name:    "empty list",
			entries: nil,
			wantErr: "at least one entry",
		},
		{
			name:    "empty entry",
			entries: []string{"wasm/js", ""},
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
			// The APE is the only native output, so a native pair is rejected and points at --cosmo-platforms.
			name:    "native pair is rejected",
			entries: []string{"darwin/amd64"},
			wantErr: "only accepts wasm targets",
		},
		{
			name:    "cosmo mixed with a native pair is rejected",
			entries: []string{"cosmo", "windows/arm64"},
			wantErr: "only accepts wasm targets",
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
			// GOOS-order spellings are a compatibility alias, normalizing to the same targets as the canonical wasm/js form.
			name:    "wasm targets GOOS-order alias",
			entries: []string{"js/wasm", "wasip1/wasm"},
			want: []buildPlatform{
				{OS: "js", Arch: "wasm"},
				{OS: "wasip1", Arch: "wasm"},
			},
		},
		{
			// Mixing the canonical spelling and its alias dedupes to a single target.
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
			name:    "wasm mixed with cosmo",
			entries: []string{"cosmo", "js/wasm"},
			want: []buildPlatform{
				{OS: "cosmo", Arch: "fat"},
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

func TestResolveMatrixPlatformsUsesTargetsWhenSet(t *testing.T) {
	oldTargets := matrixTargets
	defer func() { matrixTargets = oldTargets }()

	matrixTargets = []string{"cosmo", "wasm/js"}
	got, err := resolveMatrixPlatforms()
	require.NoError(t, err)
	assert.Equal(t, []buildPlatform{
		{OS: "cosmo", Arch: "fat"},
		{OS: "js", Arch: "wasm"},
	}, got)
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

	// The fork toolchain builds cosmo and wasm; everything else uses PATH's go.
	assert.True(t, cosmo.NeedsForkToolchain())
	assert.True(t, js.NeedsForkToolchain())
	assert.True(t, wasip1.NeedsForkToolchain())
	assert.False(t, linux.NeedsForkToolchain())
}
