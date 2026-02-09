package test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
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
	if err != nil {
		t.Fatalf("ParseProfile failed: %v", err)
	}

	if total != expectedTotal {
		t.Errorf("expected total coverage %.1f%%, got %.1f%%", expectedTotal, total)
	}

	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d", len(files))
	}
}

func TestParseProfileMissingFile(t *testing.T) {
	_, _, err := ParseProfile("/nonexistent/coverage.out")
	if err == nil {
		t.Error("expected error for missing file")
	}
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
	if err != nil {
		t.Fatalf("ParseProfile failed: %v", err)
	}

	if total != 0 {
		t.Errorf("expected 0%% coverage for empty profile, got %.1f%%", total)
	}

	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
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
	if err != nil {
		t.Fatalf("ParseProfile failed: %v", err)
	}

	// Should only have parsed the valid line
	if len(files) != 1 {
		t.Errorf("expected 1 file, got %d", len(files))
	}
	if total != 100 {
		t.Errorf("expected 100%% coverage, got %.1f%%", total)
	}
}

