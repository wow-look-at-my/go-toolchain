package test

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// generatedRe is the exact pattern used by Go tooling to detect generated files.
// See: https://pkg.go.dev/cmd/go#hdr-Generate_Go_files_by_processing_source
var generatedRe = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

// isGeneratedFile checks whether a Go source file contains the standard
// generated-code marker, using the same regexp as Go's own tooling.
func isGeneratedFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if generatedRe.MatchString(line) {
			return true
		}
		// Stop after the package clause — the marker must precede it.
		if strings.HasPrefix(line, "package ") {
			return false
		}
	}
	return false
}

// isGeneratedPackage checks if all non-test .go files in a directory are generated.
func isGeneratedPackage(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	goFiles := 0
	generatedFiles := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		goFiles++
		if isGeneratedFile(filepath.Join(dir, name)) {
			generatedFiles++
		}
	}
	return goFiles > 0 && goFiles == generatedFiles
}

// filterBlocksByGenerated removes coverage blocks whose source file is
// generated code (contains "Code generated ... DO NOT EDIT." marker).
func filterBlocksByGenerated(blocks []coverageBlock) []coverageBlock {
	cache := make(map[string]bool)
	var filtered []coverageBlock
	for _, b := range blocks {
		srcPath := findSourceFile(b.file)
		if srcPath == "" {
			// Can't resolve source — keep the block.
			filtered = append(filtered, b)
			continue
		}
		gen, ok := cache[srcPath]
		if !ok {
			gen = isGeneratedFile(srcPath)
			cache[srcPath] = gen
		}
		if !gen {
			filtered = append(filtered, b)
		}
	}
	return filtered
}
