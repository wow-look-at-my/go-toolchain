package vet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFixFileTestifyImports verifies the per-file rewrite flips the in-house
// fork back to upstream stretchr/testify (no module/network work involved).
func TestFixFileTestifyImports(t *testing.T) {
	dir := t.TempDir()
	content := `package example

import (
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestFoo(t *testing.T) {
	assert.True(t, true)
	require.True(t, true)
}
`
	filePath := filepath.Join(dir, "example_test.go")
	require.NoError(t, os.WriteFile(filePath, []byte(content), 0644))

	fixed, err := fixFileTestifyImports(filePath)
	require.NoError(t, err)
	assert.True(t, fixed)

	got, err := os.ReadFile(filePath)
	require.NoError(t, err)
	s := string(got)
	assert.Contains(t, s, `"github.com/stretchr/testify/assert"`)
	assert.Contains(t, s, `"github.com/stretchr/testify/require"`)
	assert.NotContains(t, s, `"github.com/wow-look-at-my/testify/assert"`)
	assert.NotContains(t, s, `"github.com/wow-look-at-my/testify/require"`)
}

// TestFixFileTestifyImports_AliasPreserved checks that an import alias survives
// the path rewrite (only the path string changes, not the local name).
func TestFixFileTestifyImports_AliasPreserved(t *testing.T) {
	dir := t.TempDir()
	content := `package example

import (
	"testing"

	tassert "github.com/wow-look-at-my/testify/assert"
)

func TestFoo(t *testing.T) {
	tassert.True(t, true)
}
`
	filePath := filepath.Join(dir, "example_test.go")
	require.NoError(t, os.WriteFile(filePath, []byte(content), 0644))

	fixed, err := fixFileTestifyImports(filePath)
	require.NoError(t, err)
	assert.True(t, fixed)

	got, _ := os.ReadFile(filePath)
	s := string(got)
	assert.Contains(t, s, `tassert "github.com/stretchr/testify/assert"`)
}

// TestFixFileTestifyImports_NoChanges verifies a file already on upstream is
// left untouched.
func TestFixFileTestifyImports_NoChanges(t *testing.T) {
	dir := t.TempDir()
	content := `package example

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFoo(t *testing.T) {
	assert.True(t, true)
}
`
	filePath := filepath.Join(dir, "example_test.go")
	require.NoError(t, os.WriteFile(filePath, []byte(content), 0644))

	fixed, err := fixFileTestifyImports(filePath)
	require.NoError(t, err)
	assert.False(t, fixed)
}

// TestFixTestifyImports_Orchestration exercises the full walk + module sync on a
// real temp module: a fork import becomes upstream and go.mod ends up requiring
// stretchr/testify.
func TestFixTestifyImports_Orchestration(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example\n\ngo 1.21\n"), 0644))

	content := `package example

import (
	"testing"

	"github.com/wow-look-at-my/testify/assert"
)

func TestFoo(t *testing.T) {
	assert.True(t, true)
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "example_test.go"), []byte(content), 0644))

	oldWd, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer os.Chdir(oldWd)

	fixed, err := FixTestifyImports()
	require.NoError(t, err)
	assert.True(t, fixed)

	src, _ := os.ReadFile(filepath.Join(dir, "example_test.go"))
	assert.Contains(t, string(src), `"github.com/stretchr/testify/assert"`)

	gomod, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
	assert.Contains(t, string(gomod), "github.com/stretchr/testify")
	assert.NotContains(t, string(gomod), "wow-look-at-my/testify")
}

// TestSyncVendorIfPresent_NoVendor is a no-op when there is no vendor tree.
func TestSyncVendorIfPresent_NoVendor(t *testing.T) {
	dir := t.TempDir()
	oldWd, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer os.Chdir(oldWd)

	assert.NoError(t, syncVendorIfPresent())
}
