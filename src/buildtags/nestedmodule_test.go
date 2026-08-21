package buildtags

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A nested module's packages are not import paths of the outer one, so a
// configuration derived from ITS tags names a pattern the outer module cannot
// load -- which is how `appengine`, a tag only src/compat/go-isatty carries,
// became a config this module was asked to vet itself under and could not.
func TestScanSkipsNestedModules(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module outer\n\ngo 1.24\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "own.go"),
		[]byte("//go:build mytag\n\npackage outer\n"), 0644))

	nested := filepath.Join(dir, "compat", "vendored")
	require.NoError(t, os.MkdirAll(nested, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(nested, "go.mod"), []byte("module vendored\n\ngo 1.24\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(nested, "gated.go"),
		[]byte("//go:build appengine\n\npackage vendored\n"), 0644))

	d, err := Scan(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"mytag"}, d.UserTags, "the nested module's tag must not become a configuration")
	for _, f := range d.Gated {
		assert.NotContains(t, f.Path, "vendored", "a nested module's file must not be demanded of the outer module")
	}
}
