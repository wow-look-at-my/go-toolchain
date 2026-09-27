package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The go command restarts itself by the path of the program named go, never
// by the path of this executable, which is the pipeline.
func TestGoProgramPath(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "go"+hostExeSuffix())
	require.NoError(t, os.WriteFile(link, []byte("#!/bin/sh\n"), 0o755))

	got, err := goProgramPath(link)
	require.NoError(t, err)
	assert.Equal(t, link, got)

	t.Setenv("PATH", dir)
	got, err = goProgramPath("go")
	require.NoError(t, err)
	assert.Equal(t, link, got)

	t.Setenv("PATH", t.TempDir())
	_, err = goProgramPath("go")
	assert.Error(t, err)
}

// Only the name go reaches the go command. No argv or environment makes
// go-toolchain itself the go command: dats/cli.dats pins the shell case.
func TestLinkedGoArgs(t *testing.T) {
	for _, argv := range [][]string{
		{"go-toolchain", "go", "install", "example.com/x@v1"},
		{"go-toolchain", "go", "build", "."},
		{"go-toolchain", "tool", "nosuchtool"},
		{"go-toolchain", "tool", "mytool", "-x"},
		{"go-toolchain", "matrix"},
		{"/opt/gopher", "build"},
	} {
		_, linked := LinkedGoArgs(argv)
		assert.False(t, linked, argv)
	}

	args, linked := LinkedGoArgs([]string{"/tmp/link/go", "build", "."})
	assert.True(t, linked)
	assert.Equal(t, []string{"/tmp/link/go", "build", "."}, args)

	args, linked = LinkedGoArgs([]string{`C:\link\go.exe`, "build", "."})
	assert.True(t, linked)
	assert.Equal(t, []string{`C:\link\go.exe`, "build", "."}, args)

	args, linked = LinkedGoArgs([]string{"go-toolchain", "tool", "compile", "-V=full"})
	assert.True(t, linked)
	assert.Equal(t, []string{"go-toolchain", "tool", "compile", "-V=full"}, args)
}
