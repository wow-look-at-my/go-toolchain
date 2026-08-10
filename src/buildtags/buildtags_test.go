package buildtags

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// write drops a .go file with the given build constraint into dir.
func write(t *testing.T, dir, name, constraint string) {
	t.Helper()
	body := "package p\n"
	if constraint != "" {
		body = constraint + "\n\n" + body
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
}

// A file gated by a project tag must be discovered; a platform-gated one must
// not be, because the pipeline cannot build for another GOOS and pretending
// otherwise would fail every cross-platform repo.
func TestScanSeparatesUserTagsFromPlatform(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "plain.go", "")
	write(t, dir, "gated.go", "//go:build radvdiff")
	write(t, dir, "win.go", "//go:build windows")
	write(t, dir, "cgo.go", "//go:build cgo && go1.21")

	d, err := Scan(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"radvdiff"}, d.UserTags)
	require.Len(t, d.Gated, 1)
	assert.Equal(t, "gated.go", d.Gated[0].Path)
}

// The default configuration must come first so the pipeline's primary output is
// unchanged, and every discovered tag must get a configuration of its own --
// that is what satisfies an `a && !b` shape.
func TestConfigsCoverEachTagAloneAndAllTogether(t *testing.T) {
	assert.Equal(t, []Config{{}}, configsFor(nil))

	got := configsFor([]string{"a", "b"})
	assert.Equal(t, []Config{{}, {Tags: []string{"a"}}, {Tags: []string{"b"}},
		{Tags: []string{"a", "b"}}}, got)
	assert.Empty(t, got[0].Arg(), "the default configuration passes no -tags")
	assert.Equal(t, "a,b", got[3].Arg())
}

// The guarantee: a gated file no configuration reached is reported, not
// silently skipped. This is what makes the tag impossible to hide behind.
func TestVerifyReportsUnreachedGatedFiles(t *testing.T) {
	d := &Discovery{Gated: []File{
		{Path: "a.go", Tags: []string{"x"}},
		{Path: "b.go", Tags: []string{"y"}},
	}}
	assert.Empty(t, Verify(d, map[string]bool{"a.go": true, "b.go": true}))

	missed := Verify(d, map[string]bool{"a.go": true})
	require.Len(t, missed, 1)
	assert.Equal(t, "b.go", missed[0].Path)

	err := UnreachableError(missed, "vet")
	assert.ErrorContains(t, err, "b.go")
	assert.ErrorContains(t, err, "vet could not reach")
}

// An unknown identifier must be treated as a user tag: over-covering analyzes a
// file unnecessarily, under-covering hides it, and only one of those is safe.
func TestUnknownIdentIsAUserTag(t *testing.T) {
	assert.False(t, isPlatformIdent("radvdiff"))
	assert.False(t, isPlatformIdent("ignore"))
	assert.True(t, isPlatformIdent("linux"))
	assert.True(t, isPlatformIdent("amd64"))
	assert.True(t, isPlatformIdent("cgo"))
	assert.True(t, isPlatformIdent("go1.25"))
}

// Directories the go tool never builds from must not contribute tags, or every
// repo with a tagged testdata fixture would gain a phantom configuration.
func TestScanSkipsNonBuildDirs(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"testdata", "vendor", ".git", "_ignored"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, sub), 0o755))
		write(t, filepath.Join(dir, sub), "x.go", "//go:build phantom")
	}
	d, err := Scan(dir)
	require.NoError(t, err)
	assert.Empty(t, d.UserTags)
	assert.Empty(t, d.Gated)
}
