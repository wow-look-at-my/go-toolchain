package cosmocompat

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeGoMod(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(body), 0o644))
	return dir
}

func gapFor(t *testing.T, gaps []resolvedGap, module string) resolvedGap {
	t.Helper()
	for _, g := range gaps {
		if g.gap.module == module {
			return g
		}
	}
	t.Fatalf("no resolved gap for %s in %+v", module, gaps)
	return resolvedGap{}
}

// The plain case: a require with no replace patches the module itself.
func TestNeededGaps_RequireOnly(t *testing.T) {
	dir := writeGoMod(t, `module example.com/app

go 1.25

require modernc.org/sqlite v1.50.1

require modernc.org/libc v1.72.5 // indirect
`)
	gaps, err := neededGaps(dir)
	require.NoError(t, err)

	libc := gapFor(t, gaps, "modernc.org/libc")
	assert.Equal(t, "modernc.org/libc", libc.sourceModule)
	assert.Equal(t, "v1.72.5", libc.sourceVersion)
}

// A replace onto ANOTHER MODULE is a mirror of the same code -- the module
// moved to gitlab.com/cznic. Skipping it there left buildhost's cosmo build
// to fail with twenty "build constraints exclude all Go files" lines and no
// mention of cosmocompat. The patches apply to the replacement instead.
func TestNeededGaps_ModuleReplacementIsStillPatched(t *testing.T) {
	dir := writeGoMod(t, `module example.com/app

go 1.25

require modernc.org/sqlite v1.50.1

require modernc.org/libc v1.72.5 // indirect

replace modernc.org/libc v1.72.5 => gitlab.com/cznic/libc v1.72.5

replace modernc.org/sqlite v1.50.1 => gitlab.com/cznic/sqlite v1.50.1
`)
	gaps, err := neededGaps(dir)
	require.NoError(t, err)

	libc := gapFor(t, gaps, "modernc.org/libc")
	assert.Equal(t, "gitlab.com/cznic/libc", libc.sourceModule)
	assert.Equal(t, "v1.72.5", libc.sourceVersion)
	// The go.work still replaces the path the build asks for.
	assert.Equal(t, "modernc.org/libc", libc.gap.module)
	assert.Equal(t, "v1.72.5", libc.version)

	sqlite := gapFor(t, gaps, "modernc.org/sqlite")
	assert.Equal(t, "gitlab.com/cznic/sqlite", sqlite.sourceModule)
}

// A replace onto the sqlite gap's known native fork needs no patching at
// all: that fork already ships real GOOS=cosmo support, unlike
// modernc.org/sqlite itself (see tables_sqlite.go's nativeFork field and
// src/cmd/depsforksqlite.go, which is what points a consumer's replace at
// this exact path).
func TestNeededGaps_NativeForkReplacementNeedsNoPatching(t *testing.T) {
	dir := writeGoMod(t, `module example.com/app

go 1.25

require modernc.org/sqlite v1.50.1

replace modernc.org/sqlite => github.com/wow-look-at-my/go-sqlite v1.50.1
`)
	gaps, err := neededGaps(dir)
	require.NoError(t, err)

	for _, g := range gaps {
		assert.NotEqual(t, "modernc.org/sqlite", g.gap.module, "the fork needs no runtime patching")
	}
}

// A replace onto a local directory is the consumer's own tree. cosmocompat
// cannot know what is in it and must not overwrite it, so that one is still
// skipped -- and now says so instead of leaving the build to fail unexplained.
func TestNeededGaps_DirectoryReplacementIsSkipped(t *testing.T) {
	dir := writeGoMod(t, `module example.com/app

go 1.25

require modernc.org/sqlite v1.50.1

require modernc.org/libc v1.72.5 // indirect

replace modernc.org/libc => ../my-libc
`)
	gaps, err := neededGaps(dir)
	require.NoError(t, err)

	for _, g := range gaps {
		assert.NotEqual(t, "modernc.org/libc", g.gap.module, "a directory replacement must not be patched")
	}
}

// x/sys is only reached through libc's cosmo files, so it rides along with
// libc and is skipped without it -- including when libc arrives by mirror.
func TestNeededGaps_XSysFollowsLibc(t *testing.T) {
	withLibc := writeGoMod(t, `module example.com/app

go 1.25

require (
	golang.org/x/sys v0.47.0
	modernc.org/libc v1.72.5
)

replace modernc.org/libc v1.72.5 => gitlab.com/cznic/libc v1.72.5
`)
	gaps, err := neededGaps(withLibc)
	require.NoError(t, err)
	gapFor(t, gaps, "golang.org/x/sys")

	withoutLibc := writeGoMod(t, `module example.com/app

go 1.25

require golang.org/x/sys v0.47.0
`)
	gaps, err = neededGaps(withoutLibc)
	require.NoError(t, err)
	assert.Empty(t, gaps)
}

// A staged mirror declares its own module path; the go.work replaces the path
// the consumer's build asks for, and a directory replacement must declare that
// path, so the staged copy's module line is rewritten.
func TestSetModulePath(t *testing.T) {
	dir := writeGoMod(t, `module gitlab.com/cznic/libc

go 1.25

require golang.org/x/sys v0.47.0
`)
	require.NoError(t, setModulePath(dir, "modernc.org/libc"))

	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "module modernc.org/libc")
	assert.NotContains(t, string(data), "module gitlab.com/cznic/libc")
	// Everything else survives the rewrite.
	assert.Contains(t, string(data), "golang.org/x/sys v0.47.0")

	// Already correct: unchanged.
	before, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	require.NoError(t, err)
	require.NoError(t, setModulePath(dir, "modernc.org/libc"))
	after, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
}
