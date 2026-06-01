package vet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
)

func TestMigrateGotestTools_Basic(t *testing.T) {
	dir := t.TempDir()

	content := `package example

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestFoo(t *testing.T) {
	assert.NilError(t, nil)
}
`
	filePath := filepath.Join(dir, "example_test.go")
	err := os.WriteFile(filePath, []byte(content), 0644)
	assert.Nil(t, err)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	fixed, err := migrateFileGotestTools(filePath)
	assert.Nil(t, err)
	assert.True(t, fixed)

	result, err := os.ReadFile(filePath)
	assert.Nil(t, err)

	s := string(result)
	assert.Contains(t, s, `"github.com/stretchr/testify/require"`)
	assert.NotContains(t, s, `"gotest.tools/v3/assert"`)
	assert.Contains(t, s, "require.NoError")
	assert.NotContains(t, s, "assert.NilError")
}

func TestMigrateGotestTools_FuncRenames(t *testing.T) {
	dir := t.TempDir()

	content := `package example

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestFoo(t *testing.T) {
	assert.Error(t, err, "expected msg")
	assert.DeepEqual(t, a, b)
	assert.Equal(t, a, b)
	assert.ErrorContains(t, err, "substring")
	assert.ErrorIs(t, err, target)
}
`
	filePath := filepath.Join(dir, "example_test.go")
	err := os.WriteFile(filePath, []byte(content), 0644)
	assert.Nil(t, err)

	fixed, err := migrateFileGotestTools(filePath)
	assert.Nil(t, err)
	assert.True(t, fixed)

	result, err := os.ReadFile(filePath)
	assert.Nil(t, err)

	s := string(result)
	assert.Contains(t, s, "require.EqualError")
	assert.Contains(t, s, "require.ErrorContains")
	assert.Contains(t, s, "require.ErrorIs")
	// DeepEqual → Equal, original Equal stays Equal
	assert.NotContains(t, s, "DeepEqual")
	// Should have require.Equal (from both Equal and DeepEqual)
	assert.Contains(t, s, "require.Equal")
}

func TestMigrateGotestTools_Assert(t *testing.T) {
	dir := t.TempDir()

	content := "package example\n\nimport (\n\t\"testing\"\n\n\t\"gotest.tools/v3/assert\"\n)\n\nfunc TestFoo(t *testing.T) {\n\tassert.Assert(t, len(items) > 0)\n}\n"
	filePath := filepath.Join(dir, "example_test.go")
	err := os.WriteFile(filePath, []byte(content), 0644)
	assert.Nil(t, err)

	fixed, err := migrateFileGotestTools(filePath)
	assert.Nil(t, err)
	assert.True(t, fixed)

	result, err := os.ReadFile(filePath)
	assert.Nil(t, err)

	s := string(result)
	assert.Contains(t, s, "require.True")
	assert.NotContains(t, s, "require.Assert")
}

func TestMigrateGotestTools_NoDuplicateImport(t *testing.T) {
	dir := t.TempDir()

	// File that already has testify/require AND gotest.tools/assert
	content := "package example\n\nimport (\n\t\"testing\"\n\n\t\"github.com/stretchr/testify/require\"\n\t\"gotest.tools/v3/assert\"\n)\n\nfunc TestFoo(t *testing.T) {\n\trequire.NoError(t, nil)\n\tassert.NilError(t, nil)\n}\n"
	filePath := filepath.Join(dir, "example_test.go")
	err := os.WriteFile(filePath, []byte(content), 0644)
	assert.Nil(t, err)

	fixed, err := migrateFileGotestTools(filePath)
	assert.Nil(t, err)
	assert.True(t, fixed)

	result, err := os.ReadFile(filePath)
	assert.Nil(t, err)

	s := string(result)
	// Should have exactly one require import, not two
	count := strings.Count(s, `"github.com/stretchr/testify/require"`)
	assert.Equal(t, 1, count, "should have exactly one require import, got %d", count)
}

func TestMigrateGotestTools_NoChanges(t *testing.T) {
	dir := t.TempDir()

	content := `package example

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFoo(t *testing.T) {
	require.NoError(t, nil)
}
`
	filePath := filepath.Join(dir, "example_test.go")
	err := os.WriteFile(filePath, []byte(content), 0644)
	assert.Nil(t, err)

	fixed, err := migrateFileGotestTools(filePath)
	assert.Nil(t, err)
	assert.False(t, fixed)
}

func TestMigrateGotestTools_Check(t *testing.T) {
	dir := t.TempDir()

	content := `package example

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestFoo(t *testing.T) {
	assert.NilError(t, nil)
	assert.Check(t, someExpr)
}
`
	filePath := filepath.Join(dir, "example_test.go")
	err := os.WriteFile(filePath, []byte(content), 0644)
	assert.Nil(t, err)

	fixed, err := migrateFileGotestTools(filePath)
	assert.Nil(t, err)
	assert.True(t, fixed)

	result, err := os.ReadFile(filePath)
	assert.Nil(t, err)

	s := string(result)
	// NilError → require.NoError (fatal)
	assert.Contains(t, s, "require.NoError")
	// Check → assert.True (non-fatal)
	assert.Contains(t, s, "assert.True")
	// Should have added testify/assert import
	assert.Contains(t, s, `"github.com/stretchr/testify/assert"`)
	// Should have testify/require import
	assert.Contains(t, s, `"github.com/stretchr/testify/require"`)
}

func TestMigrateGotestTools_CmpUnwrap(t *testing.T) {
	dir := t.TempDir()

	content := `package example

import (
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestFoo(t *testing.T) {
	assert.NilError(t, nil)
	assert.Check(t, cmp.Equal(a, b))
	assert.Assert(t, cmp.Nil(x))
	assert.Assert(t, cmp.ErrorContains(err, "msg"))
}
`
	filePath := filepath.Join(dir, "example_test.go")
	err := os.WriteFile(filePath, []byte(content), 0644)
	assert.Nil(t, err)

	fixed, err := migrateFileGotestTools(filePath)
	assert.Nil(t, err)
	assert.True(t, fixed)

	result, err := os.ReadFile(filePath)
	assert.Nil(t, err)

	s := string(result)
	// cmp import should be removed
	assert.NotContains(t, s, `gotest.tools/v3/assert/cmp`)
	// cmp.Equal unwrapped: Check → assert.Equal (non-fatal)
	assert.Contains(t, s, "assert.Equal(t, a, b)")
	// cmp.Nil unwrapped: Assert → require.Nil (fatal)
	assert.Contains(t, s, "require.Nil(t, x)")
	// cmp.ErrorContains unwrapped: Assert → require.ErrorContains (fatal)
	assert.Contains(t, s, `require.ErrorContains(t, err, "msg")`)
	// No remaining cmp references
	assert.NotContains(t, s, "cmp.")
}
