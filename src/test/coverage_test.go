package test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

)

func TestParseProfile(t *testing.T) {
	tmpDir := t.TempDir()
	coverFile := filepath.Join(tmpDir, "coverage.out")

	// Build test data with known coverage:
	// file1: 2 statements, 1 covered (50%)
	// file2: 2 statements, 2 covered (100%)
	// total: 4 statements, 3 covered (75%)
	lines := []struct {
		file       string
		statements int
		covered    bool
	}{
		{"example.com/pkg/file1.go:10.20,12.2", 1, true},
		{"example.com/pkg/file1.go:14.20,16.2", 1, false},
		{"example.com/pkg/file2.go:10.20,12.2", 1, true},
		{"example.com/pkg/file2.go:14.20,16.2", 1, true},
	}

	var totalStmts, coveredStmts int
	content := "mode: set\n"
	for _, l := range lines {
		count := 0
		if l.covered {
			count = 1
			coveredStmts += l.statements
		}
		totalStmts += l.statements
		content += fmt.Sprintf("%s %d %d\n", l.file, l.statements, count)
	}

	expectedTotal := float32(coveredStmts) / float32(totalStmts) * 100

	if err := os.WriteFile(coverFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write coverage file: %v", err)
	}

	total, files, err := ParseProfile(coverFile)
	require.Nil(t, err)

	assert.Equal(t, expectedTotal, total)

	assert.Equal(t, 2, len(files))
}

func TestParseProfileMissingFile(t *testing.T) {
	_, _, err := ParseProfile("/nonexistent/coverage.out")
	assert.NotNil(t, err)
}

func TestParseProfileEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	coverFile := filepath.Join(tmpDir, "coverage.out")

	content := `mode: set
`
	if err := os.WriteFile(coverFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write coverage file: %v", err)
	}

	total, files, err := ParseProfile(coverFile)
	require.Nil(t, err)

	assert.Equal(t, 0, total)

	assert.Equal(t, 0, len(files))
}

func TestParseProfileMalformedLines(t *testing.T) {
	tmpDir := t.TempDir()
	coverFile := filepath.Join(tmpDir, "coverage.out")

	// Various malformed lines that should be skipped
	content := `mode: set
not enough fields
too many fields here now extra
no-colon-in-path 1 1
example.com/pkg/file.go:10.20,12.2 1 1
`
	if err := os.WriteFile(coverFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write coverage file: %v", err)
	}

	total, files, err := ParseProfile(coverFile)
	require.Nil(t, err)

	// Should only have parsed the valid line
	assert.Equal(t, 1, len(files))
	assert.Equal(t, 100, total)
}

