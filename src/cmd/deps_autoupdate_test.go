package cmd

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetAutoUpdatePrefix_ValidModule(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	t.Chdir(dir)

	os.WriteFile("go.mod", []byte("module github.com/wow-look-at-my/go-toolchain\ngo 1.21\n"), 0644)
	assert.Equal(t, "github.com/wow-look-at-my/", getAutoUpdatePrefix())
}

func TestGetAutoUpdatePrefix_GitLabModule(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	t.Chdir(dir)

	os.WriteFile("go.mod", []byte("module gitlab.com/group/repo\ngo 1.21\n"), 0644)
	assert.Equal(t, "gitlab.com/group/", getAutoUpdatePrefix())
}

func TestGetAutoUpdatePrefix_NoGoMod(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	t.Chdir(dir)

	assert.Equal(t, "", getAutoUpdatePrefix())
}

func TestGetAutoUpdatePrefix_SingleComponent(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	t.Chdir(dir)

	os.WriteFile("go.mod", []byte("module localhost\ngo 1.21\n"), 0644)
	assert.Equal(t, "", getAutoUpdatePrefix())
}

func TestGetAutoUpdatePrefix_MalformedGoMod(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	t.Chdir(dir)

	os.WriteFile("go.mod", []byte("not valid go.mod content {{{"), 0644)
	assert.Equal(t, "", getAutoUpdatePrefix())
}

func TestGetAutoUpdatePrefix_EmptyGoMod(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	t.Chdir(dir)

	os.WriteFile("go.mod", []byte(""), 0644)
	assert.Equal(t, "", getAutoUpdatePrefix())
}
