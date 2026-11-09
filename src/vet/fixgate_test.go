package vet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/buildtags"
)

// TestGateRevertsAPackageThatNoLongerCompiles writes a fix that breaks one
// package and a fix that keeps another whole. The gate must put the broken
// package back, refuse a second fix to it, and say so in its error. It must
// leave the good fix alone.
func TestGateRevertsAPackageThatNoLongerCompiles(t *testing.T) {
	t.Serial("the gate loads packages from the working directory")
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module gatemod\n\ngo 1.21\n"), 0644))
	broken := filepath.Join(dir, "broken", "broken.go")
	good := filepath.Join(dir, "good", "good.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(broken), 0755))
	require.NoError(t, os.MkdirAll(filepath.Dir(good), 0755))
	brokenSrc := "package broken\n\nfunc F() int { return 1 }\n"
	goodSrc := "package good\n\nfunc G() int { return 1 }\n"
	require.NoError(t, os.WriteFile(broken, []byte(brokenSrc), 0644))
	require.NoError(t, os.WriteFile(good, []byte(goodSrc), 0644))
	t.Chdir(dir)

	ed := NewEditor(true)
	wrote, err := ed.Apply(broken, []byte("package broken\n\nimport \"strings\"\n\nfunc F() int { return 1 }\n"))
	require.NoError(t, err)
	require.True(t, wrote)
	fixedGood := "package good\n\nfunc G() int { return 2 }\n"
	wrote, err = ed.Apply(good, []byte(fixedGood))
	require.NoError(t, err)
	require.True(t, wrote)

	reverted, err := gateFixedPackages(ed, buildtags.Config{})
	require.NoError(t, err)
	assert.True(t, reverted)

	content, err := os.ReadFile(broken)
	require.NoError(t, err)
	assert.Equal(t, brokenSrc, string(content), "the fix that broke the package is undone")
	content, err = os.ReadFile(good)
	require.NoError(t, err)
	assert.Equal(t, fixedGood, string(content), "a fix that compiles stays")

	wrote, err = ed.Apply(broken, []byte("package broken\n\nfunc F() int { return 3 }\n"))
	require.NoError(t, err)
	assert.False(t, wrote, "a reverted file takes no further fix this run")

	re := ed.(*applyEditor).revertErr()
	require.Error(t, re)
	assert.Contains(t, re.Error(), "auto-fix reverted")
	assert.Contains(t, re.Error(), "broken.go")
	assert.Contains(t, re.Error(), `"strings" imported and not used`)

	reverted, err = gateFixedPackages(ed, buildtags.Config{})
	require.NoError(t, err)
	assert.False(t, reverted, "nothing was written since the last gate")
}
