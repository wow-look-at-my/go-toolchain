package cmd

import (
	"os"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
)

func TestGetAutoUpdatePrefix_ValidModule(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	os.WriteFile("go.mod", []byte("module github.com/wow-look-at-my/go-toolchain\ngo 1.21\n"), 0644)
	assert.Equal(t, "github.com/wow-look-at-my/", getAutoUpdatePrefix())
}

func TestGetAutoUpdatePrefix_GitLabModule(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	os.WriteFile("go.mod", []byte("module gitlab.com/group/repo\ngo 1.21\n"), 0644)
	assert.Equal(t, "gitlab.com/group/", getAutoUpdatePrefix())
}

func TestGetAutoUpdatePrefix_NoGoMod(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	assert.Equal(t, "", getAutoUpdatePrefix())
}

func TestGetAutoUpdatePrefix_SingleComponent(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	os.WriteFile("go.mod", []byte("module localhost\ngo 1.21\n"), 0644)
	assert.Equal(t, "", getAutoUpdatePrefix())
}

func TestGetAutoUpdatePrefix_MalformedGoMod(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	os.WriteFile("go.mod", []byte("not valid go.mod content {{{"), 0644)
	assert.Equal(t, "", getAutoUpdatePrefix())
}

func TestGetAutoUpdatePrefix_EmptyGoMod(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	os.WriteFile("go.mod", []byte(""), 0644)
	assert.Equal(t, "", getAutoUpdatePrefix())
}
