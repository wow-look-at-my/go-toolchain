package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A windows slot copy of a shebang APE is an artifact that runs nowhere:
// the heading it carries is the one Windows cannot load, and the copy is
// published under a name promising it can. That combination must fail the
// build, not warn during it.
func TestCheckCosmoShebangSlots(t *testing.T) {
	slots := func(entries ...string) []buildPlatform {
		t.Helper()
		p, err := parseCosmoSlots(entries)
		require.NoError(t, err)
		return p
	}

	for _, tt := range []struct {
		name    string
		shebang bool
		slots   []buildPlatform
		wantErr string
	}{
		{name: "off, windows slot", shebang: false, slots: slots("linux/amd64", "windows/amd64")},
		{name: "off, defaults", shebang: false, slots: slots(DefaultCosmoSlots...)},
		{name: "on, unix slots", shebang: true, slots: slots("linux/amd64", "linux/arm64", "darwin/arm64")},
		{name: "on, no slots", shebang: true, slots: slots("none")},
		{
			name: "on, windows slot", shebang: true, slots: slots("linux/amd64", "windows/amd64"),
			wantErr: "windows/amd64",
		},
		{
			// The default slot set includes windows/amd64, so plain
			// --cosmo-shebang with no --cosmo-slots must fail rather than
			// quietly publishing a broken windows artifact.
			name: "on, default slots", shebang: true, slots: slots(DefaultCosmoSlots...),
			wantErr: "--cosmo-shebang cannot be combined with the windows/amd64 cosmo slot",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := checkCosmoShebangSlots(tt.shebang, tt.slots)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// A fork toolchain older than GOCOSMOSHEBANG ignores the environment variable
// and writes an ordinary MZ APE. Nothing downstream would notice until the
// artifact reached a machine that tried to spawn it -- which is exactly the
// machine that cannot run a shell first -- so the build must catch it.
func TestVerifyCosmoShebang(t *testing.T) {
	write := func(t *testing.T, head string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "app_cosmo_fat")
		require.NoError(t, os.WriteFile(p, []byte(head+"rest of the binary"), 0o755))
		return p
	}

	t.Run("shebang heading passes", func(t *testing.T) {
		job := buildJob{outputPath: write(t, "#!/bin/sh\n"), cosmoShebang: true}
		require.NoError(t, verifyCosmoShebang(job))
	})

	t.Run("MZ heading fails with an actionable message", func(t *testing.T) {
		job := buildJob{outputPath: write(t, "MZqFpD='\n"), cosmoShebang: true, forkGoroot: "/opt/fork"}
		err := verifyCosmoShebang(job)
		require.Error(t, err)
		require.Contains(t, err.Error(), "predates GOCOSMOSHEBANG")
		require.Contains(t, err.Error(), "/opt/fork")
	})

	t.Run("not requested, not checked", func(t *testing.T) {
		job := buildJob{outputPath: write(t, "MZqFpD='\n")}
		require.NoError(t, verifyCosmoShebang(job))
		// Not even a missing file is an error when the flag is off.
		require.NoError(t, verifyCosmoShebang(buildJob{outputPath: "/nonexistent/app"}))
	})

	t.Run("missing output is an error when requested", func(t *testing.T) {
		require.Error(t, verifyCosmoShebang(buildJob{outputPath: "/nonexistent/app", cosmoShebang: true}))
	})
}
