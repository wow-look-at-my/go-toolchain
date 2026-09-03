package vet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSourceLocationShortLocAbsolute(t *testing.T) {
	cwd, _ := os.Getwd()
	absPath := "/nonexistent/path/file.go"
	loc := SourceLocation{File: absPath, Line: 10}
	short := loc.ShortLoc()

	// A path outside cwd still relates to it, unless the host roots them differently.
	expected, err := filepath.Rel(cwd, absPath)
	if err != nil {
		expected = absPath
	}
	assert.Equal(t, expected+":10", short)
}
