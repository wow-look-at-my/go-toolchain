package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The default matrix builds ONE fat APE, not a per-platform binary each. This
// is the whole point of the change: no target flags means one artifact.
func TestResolveMatrixPlatformsDefaultsToOneAPE(t *testing.T) {
	oldTargets := matrixTargets
	defer func() { matrixTargets = oldTargets }()
	matrixTargets = nil

	got, err := resolveMatrixPlatforms()
	require.NoError(t, err)
	assert.Equal(t, []buildPlatform{{OS: cosmoOS, Arch: cosmoFatArch}}, got)
}

func TestParseCosmoPlatforms(t *testing.T) {
	tests := []struct {
		name    string
		entries []string
		want    []buildPlatform
		wantErr string
	}{
		{
			name:    "default set",
			entries: DefaultCosmoPlatforms,
			want: []buildPlatform{
				{OS: "linux", Arch: "amd64"},
				{OS: "darwin", Arch: "arm64"},
				{OS: "windows", Arch: "amd64"},
			},
		},
		{
			name:    "all leaves the variable unset",
			entries: []string{"all"},
			want:    nil,
		},
		{
			name:    "single platform",
			entries: []string{"linux/amd64"},
			want:    []buildPlatform{{OS: "linux", Arch: "amd64"}},
		},
		{
			// The set is what tells a consumer where the binary runs, so a
			// platform whose runtime was never proven cannot be in it.
			name:    "unverified darwin/amd64 is refused",
			entries: []string{"darwin/amd64"},
			wantErr: "darwin-Intel runtime is unverified",
		},
		{
			name:    "windows/arm64 is refused",
			entries: []string{"windows/arm64"},
			wantErr: "PE payload is amd64-only",
		},
		{
			name:    "a platform the APE never covers",
			entries: []string{"freebsd/amd64"},
			wantErr: "the fat APE covers darwin/arm64, linux/amd64, linux/arm64, windows/amd64",
		},
		{
			name:    "wasm is not a host",
			entries: []string{"wasm/js"},
			wantErr: "wasm is not one",
		},
		{
			name:    "all mixed with a platform is rejected",
			entries: []string{"all", "linux/amd64"},
			wantErr: `"all" must be the only value`,
		},
		{
			name:    "duplicate",
			entries: []string{"linux/amd64", "linux/amd64"},
			wantErr: "duplicate --cosmo-platforms",
		},
		{
			name:    "empty",
			entries: nil,
			wantErr: "would run nowhere",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCosmoPlatforms(tt.entries)
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

// "all" publishes the platforms the APE is PROVEN to run on, never the extra
// ones the fork can also emit — the published set is a promise.
func TestApeCoverageForAll(t *testing.T) {
	assert.Equal(t, []buildPlatform{
		{OS: "darwin", Arch: "arm64"},
		{OS: "linux", Arch: "amd64"},
		{OS: "linux", Arch: "arm64"},
		{OS: "windows", Arch: "amd64"},
	}, apeCoverage(nil))

	explicit := []buildPlatform{{OS: "linux", Arch: "amd64"}}
	assert.Equal(t, explicit, apeCoverage(explicit))
}

func TestPlatformList(t *testing.T) {
	assert.Equal(t, "linux/amd64,darwin/arm64", platformList([]buildPlatform{
		{OS: "linux", Arch: "amd64"},
		{OS: "darwin", Arch: "arm64"},
	}))
	assert.Equal(t, "", platformList(nil))
}

// A toolchain that cannot restrict coverage must SAY SO. Reporting a slimmed
// build that was not slimmed is the failure this warning exists to prevent.
func TestCosmoPlatformsEnvValue(t *testing.T) {
	old := cosmoPlatformsSupportedFunc
	defer func() { cosmoPlatformsSupportedFunc = old }()
	platforms := []buildPlatform{{OS: "linux", Arch: "amd64"}, {OS: "darwin", Arch: "arm64"}}

	t.Run("supported", func(t *testing.T) {
		cosmoPlatformsSupportedFunc = func(string) bool { return true }
		out := captureCombinedOutput(func() {
			assert.Equal(t, "linux/amd64,darwin/arm64", cosmoPlatformsEnvValue("/fork", platforms))
		})
		assert.NotContains(t, out, "Warning")
	})

	t.Run("unsupported warns and leaves it unset", func(t *testing.T) {
		cosmoPlatformsSupportedFunc = func(string) bool { return false }
		var got string
		out := captureCombinedOutput(func() { got = cosmoPlatformsEnvValue("/fork", platforms) })
		assert.Equal(t, "", got, "an ignored variable must not be set at all")
		assert.Contains(t, out, cosmoPlatformsEnv)
		assert.Contains(t, out, "linux/amd64,darwin/arm64")
	})

	t.Run("all needs no probe", func(t *testing.T) {
		cosmoPlatformsSupportedFunc = func(string) bool {
			t.Fatal("the probe must not run when no platform set was requested")
			return false
		}
		out := captureCombinedOutput(func() {
			assert.Equal(t, "", cosmoPlatformsEnvValue("/fork", nil))
		})
		assert.NotContains(t, out, "Warning")
	})
}
