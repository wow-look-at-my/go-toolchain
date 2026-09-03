package vet

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFixFileTestifyImports verifies the per-file rewrite flips the in-house
// fork back to upstream stretchr/testify (no module/network work involved).
func TestFixFileTestifyImports(t *testing.T) {
	t.Parallel() // renderTestifyImports takes an explicit file path; no cwd, no process-wide state.
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

	newSrc, changes, err := renderTestifyImports(filePath)
	require.NoError(t, err)
	assert.Len(t, changes, 2)

	s := string(newSrc)
	assert.Contains(t, s, `"github.com/stretchr/testify/assert"`)
	assert.Contains(t, s, `"github.com/stretchr/testify/require"`)
	assert.NotContains(t, s, `"github.com/wow-look-at-my/testify/assert"`)
	assert.NotContains(t, s, `"github.com/wow-look-at-my/testify/require"`)
}

// TestFixFileTestifyImports_AliasPreserved checks that an import alias survives
// the path rewrite (only the path string changes, not the local name).
func TestFixFileTestifyImports_AliasPreserved(t *testing.T) {
	t.Parallel() // See TestFixFileTestifyImports.
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

	newSrc, changes, err := renderTestifyImports(filePath)
	require.NoError(t, err)
	assert.Len(t, changes, 1)

	assert.Contains(t, string(newSrc), `tassert "github.com/stretchr/testify/assert"`)
}

// TestFixFileTestifyImports_NoChanges verifies a file already on upstream is
// left untouched.
func TestFixFileTestifyImports_NoChanges(t *testing.T) {
	t.Parallel() // See TestFixFileTestifyImports.
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

	newSrc, changes, err := renderTestifyImports(filePath)
	require.NoError(t, err)
	assert.Empty(t, changes)
	assert.Nil(t, newSrc)
}

// TestFixTestifyImports_CheckModeRejectsFork is the CI-enforcement regression
// guard: in check mode (fix=false, the CI path) a file importing the fork must
// be reported as a hard error and must NOT be rewritten. This is the behavior
// that was missing — CI used to skip the migration entirely and pass green on
// the fork.
func TestFixTestifyImports_CheckModeRejectsFork(t *testing.T) {
	dir := t.TempDir()
	content := `package example

import (
	"testing"

	"github.com/wow-look-at-my/testify/assert"
)

func TestFoo(t *testing.T) {
	assert.True(t, true)
}
`
	filePath := filepath.Join(dir, "example_test.go")
	require.NoError(t, os.WriteFile(filePath, []byte(content), 0644))

	t.Chdir(dir)

	ed := NewEditor(false)
	wrote, err := FixTestifyImports(ed)
	require.NoError(t, err)
	assert.False(t, wrote)
	require.Error(t, ed.Err())
	assert.Contains(t, ed.Err().Error(), "example_test.go")
	assert.Contains(t, ed.Err().Error(), "wow-look-at-my/testify")

	// Check mode must not write: the fork import is still present, untouched.
	got, readErr := os.ReadFile(filePath)
	require.NoError(t, readErr)
	assert.Contains(t, string(got), "github.com/wow-look-at-my/testify/assert")
	assert.NotContains(t, string(got), "github.com/stretchr/testify/assert")
}

// TestFixTestifyImports_CheckModeCleanPasses verifies check mode is a quiet
// no-op (no error, nothing changed) when no file imports the fork.
func TestFixTestifyImports_CheckModeCleanPasses(t *testing.T) {
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, "example_test.go"), []byte(content), 0644))

	t.Chdir(dir)

	ed := NewEditor(false)
	wrote, err := FixTestifyImports(ed)
	require.NoError(t, err)
	assert.False(t, wrote)
	require.NoError(t, ed.Err())
}

// TestFixTestifyImports_Orchestration exercises the full walk + module sync on a
// temp module: a fork import becomes upstream and go.mod ends up requiring
// stretchr/testify. The rewrite runs before any go mod tidy, so upstream
// testify is resolved via a local stub replace (no network needed).
func TestFixTestifyImports_Orchestration(t *testing.T) {
	stub, err := filepath.Abs(filepath.Join("testdata", "src", "testifystub"))
	require.NoError(t, err)

	dir := t.TempDir()
	gomodContent := "module example\n\ngo 1.21\n\nrequire github.com/stretchr/testify v1.9.0\n\nreplace github.com/stretchr/testify => " + stub + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomodContent), 0644))

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

	t.Chdir(dir)

	wrote, err := FixTestifyImports(NewEditor(true))
	require.NoError(t, err)
	assert.True(t, wrote)

	src, _ := os.ReadFile(filepath.Join(dir, "example_test.go"))
	assert.Contains(t, string(src), `"github.com/stretchr/testify/assert"`)

	gomod, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
	assert.Contains(t, string(gomod), "github.com/stretchr/testify")
	assert.NotContains(t, string(gomod), "wow-look-at-my/testify")
}

// TestSyncVendorIfPresent_NoVendor is a no-op when there is no vendor tree.
func TestSyncVendorIfPresent_NoVendor(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	t.Chdir(dir)

	assert.NoError(t, syncVendorIfPresent())
}

// TestFixTestifyImports_VendorConsistency builds a vendored module on the fork,
// runs the rewriter, and verifies the result is a consistent vendor tree on
// upstream testify: vendor/modules.txt no longer references the fork, and
// `go build -mod=vendor ./...` and `go vet -mod=vendor ./...` succeed. This is
// the regression guard for the "inconsistent vendoring" failure the old
// fork-direction rewrite produced. Both testify modules resolve to local stubs
// via replace directives so the test is hermetic and fast (no network, which
// the per-package test timeout cannot afford).
func TestFixTestifyImports_VendorConsistency(t *testing.T) {
	forkStub, err := filepath.Abs(filepath.Join("testdata", "src", "forkstub"))
	require.NoError(t, err)
	upstreamStub, err := filepath.Abs(filepath.Join("testdata", "src", "testifystub"))
	require.NoError(t, err)

	dir := t.TempDir()
	write := func(name, content string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0644))
	}
	write("go.mod", "module vendoredexample\n\ngo 1.21\n\n"+
		"require github.com/wow-look-at-my/testify v1.9.0\n\n"+
		"replace github.com/wow-look-at-my/testify => "+forkStub+"\n\n"+
		"replace github.com/stretchr/testify => "+upstreamStub+"\n")
	write("foo.go", "package vendoredexample\n\n// Foo exists so the package has a non-test file to build.\nfunc Foo() int { return 1 }\n")
	write("foo_test.go", `package vendoredexample

import (
	"testing"

	"github.com/wow-look-at-my/testify/assert"
)

func TestFoo(t *testing.T) {
	assert.Equal(t, 1, Foo())
}
`)

	t.Chdir(dir)

	// Set up a vendored state on the fork (resolved via the local stub).
	for _, args := range [][]string{{"mod", "tidy"}, {"mod", "vendor"}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "vendor setup (go %v):\n%s", args, out)
	}
	modulesTxt := filepath.Join(dir, "vendor", "modules.txt")
	pre, err := os.ReadFile(modulesTxt)
	require.NoError(t, err)
	require.Contains(t, string(pre), "github.com/wow-look-at-my/testify/assert", "fixture should start with the fork vendored")

	// Run the rewriter: flip imports to upstream and resync the vendor tree.
	wrote, err := FixTestifyImports(NewEditor(true))
	require.NoError(t, err)
	assert.True(t, wrote)

	// Imports flipped in source.
	src, _ := os.ReadFile(filepath.Join(dir, "foo_test.go"))
	assert.Contains(t, string(src), "github.com/stretchr/testify/assert")
	assert.NotContains(t, string(src), "wow-look-at-my/testify")

	// go.mod now requires upstream testify; the fork is no longer vendored.
	gomod, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
	assert.Contains(t, string(gomod), "require github.com/stretchr/testify")
	post, err := os.ReadFile(modulesTxt)
	require.NoError(t, err)
	assert.Contains(t, string(post), "github.com/stretchr/testify/assert")
	assert.NotContains(t, string(post), "github.com/wow-look-at-my/testify/assert")
	_, statErr := os.Stat(filepath.Join(dir, "vendor", "github.com", "wow-look-at-my"))
	assert.True(t, os.IsNotExist(statErr), "fork must not remain in the vendor tree")

	// The vendored module builds and vets with -mod=vendor (no "inconsistent
	// vendoring" error).
	for _, args := range [][]string{{"build", "-mod=vendor", "./..."}, {"vet", "-mod=vendor", "./..."}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		assert.NoError(t, err, "go %v failed:\n%s", args, out)
	}
}
