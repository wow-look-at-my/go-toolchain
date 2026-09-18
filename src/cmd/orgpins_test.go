package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeOrgPinFiles lays out a repository under a temporary root.
func writeOrgPinFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
	return root
}

func TestFindOrgPinsAcceptsThePlaceholder(t *testing.T) {
	root := writeOrgPinFiles(t, map[string]string{
		"go.mod": `module example.com/m

go 1.21

require (
	github.com/wow-look-at-my/dep v0.0.0
	github.com/wow-look-at-my/dep/v2 v2.0.0 // indirect
	github.com/wow-look-at-my/forked v0.0.0 // branch=v1
	github.com/pierrec/lz4/v4 v4.1.27
)
`,
		"go.sum":              "github.com/pierrec/lz4/v4 v4.1.27 h1:abc=\n",
		"vendor/modules.txt":  "# github.com/wow-look-at-my/dep v0.0.0\n## explicit; go 1.21\n",
		".gitmodules":         "[submodule \"dep\"]\n\tpath = dep\n\turl = https://github.com/wow-look-at-my/dep.git\n\tbranch = master\n",
		".github/workflows/ci.yml": `jobs:
  test:
    steps:
      - uses: wow-look-at-my/dats@master
      - uses: wow-look-at-my/actions@secret-server#latest
      - uses: actions/checkout@v7
`,
	})

	pins, err := findOrgPins(root)
	require.NoError(t, err)
	assert.Empty(t, pins)
	assert.NoError(t, checkOrgPins(root))
}

func TestFindOrgPinsRefusesAFrozenVersion(t *testing.T) {
	root := writeOrgPinFiles(t, map[string]string{
		"go.mod": `module example.com/m

go 1.21

require (
	github.com/wow-look-at-my/dated v0.0.0-20260913211206-5bf638fdce71
	github.com/wow-look-at-my/tagged v1.4.0 // indirect
)
`,
		"go.sum": "github.com/wow-look-at-my/dated v0.0.0-20260913211206-5bf638fdce71/go.mod h1:abc=\n",
	})

	pins, err := findOrgPins(root)
	require.NoError(t, err)
	require.Len(t, pins, 3)
	assert.Equal(t, "go.mod:6: org module pinned to v0.0.0-20260913211206-5bf638fdce71", pins[0].String())
	assert.Equal(t, "go.mod:7: org module pinned to v1.4.0", pins[1].String())
	assert.Equal(t, "go.sum", pins[2].File)

	err = checkOrgPins(root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "org dependencies are pinned")
}

func TestFindOrgPinsRefusesASubmoduleWithNoBranch(t *testing.T) {
	root := writeOrgPinFiles(t, map[string]string{
		".gitmodules": "[submodule \"dep\"]\n\tpath = dep\n\turl = https://github.com/wow-look-at-my/dep.git\n" +
			"[submodule \"lz4\"]\n\tpath = lz4\n\turl = https://github.com/pierrec/lz4.git\n",
	})

	pins, err := findOrgPins(root)
	require.NoError(t, err)
	require.Len(t, pins, 1)
	assert.Equal(t, ".gitmodules:1: submodule dep names no branch to follow", pins[0].String())
}

func TestFindOrgPinsRefusesAnActionAtATagOrACommit(t *testing.T) {
	root := writeOrgPinFiles(t, map[string]string{
		".github/workflows/ci.yml": `jobs:
  test:
    steps:
      - uses: wow-look-at-my/dats@v1
      - uses: wow-look-at-my/dats@0123456789abcdef0123456789abcdef01234567
`,
	})

	pins, err := findOrgPins(root)
	require.NoError(t, err)
	require.Len(t, pins, 2)
	assert.Contains(t, pins[0].String(), "org action pinned to v1")
	assert.Contains(t, pins[1].String(), "org action pinned to 0123456789abcdef0123456789abcdef01234567")
}

func TestIsOrgPlaceholder(t *testing.T) {
	for _, version := range []string{"v0.0.0", "v2.0.0", "v11.0.0", "v0.0.0/go.mod"} {
		assert.True(t, isOrgPlaceholder(version), version)
	}
	for _, version := range []string{"v1.4.0", "v0.0.1", "v0.0.0-20260913211206-5bf638fdce71", "v0.0.0-rc1", "vX.0.0"} {
		assert.False(t, isOrgPlaceholder(version), version)
	}
}
