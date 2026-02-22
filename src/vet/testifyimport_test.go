package vet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
)

func TestFixTestifyImports(t *testing.T) {
	dir := t.TempDir()

	// Create go.mod
	goMod := "module example\n\ngo 1.21\n"
	err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644)
	assert.Nil(t, err)

	// Create a file with broken import
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
	err = os.WriteFile(filePath, []byte(content), 0644)
	assert.Nil(t, err)

	// Change to temp dir
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	// Run the fixer
	fixed, err := FixTestifyImports()
	assert.Nil(t, err)
	assert.True(t, fixed)

	// Verify the import was fixed
	newContent, err := os.ReadFile(filePath)
	assert.Nil(t, err)
	assert.Contains(t, string(newContent), `"github.com/wow-look-at-my/testify/assert"`)
	assert.NotContains(t, string(newContent), `"github.com/stretchr/testify/assert"`)
}

func TestFixTestifyImports_NoChanges(t *testing.T) {
	dir := t.TempDir()

	// Create a file with correct import
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
	err := os.WriteFile(filePath, []byte(content), 0644)
	assert.Nil(t, err)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	fixed, err := FixTestifyImports()
	assert.Nil(t, err)
	assert.False(t, fixed)
}
