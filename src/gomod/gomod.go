// Package gomod provides filesystem-based Go module utilities that replace
// go list invocations with direct parsing of go.mod and source files.
package gomod

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// ReadModulePath reads the module path from go.mod in the current directory.
func ReadModulePath() string {
	f, err := os.Open("go.mod")
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module"))
		}
	}
	return ""
}

// skipDir returns true if the directory name should be skipped during walks.
func skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" || name == "node_modules"
}

// FindMainPackages walks the filesystem from the current directory to find
// all directories containing non-test .go files with "package main" declarations,
// returning their import paths (module path + relative directory).
func FindMainPackages() ([]string, error) {
	modPath := ReadModulePath()
	if modPath == "" {
		return nil, nil
	}

	var pkgs []string
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable dirs
		}
		if !d.IsDir() {
			return nil
		}
		// Skip hidden dirs, vendor, testdata, node_modules
		name := d.Name()
		if name != "." && skipDir(name) {
			return filepath.SkipDir
		}

		// Check if this directory has a non-test .go file with package main
		if hasMainPackage(path) {
			importPath := modPath
			if path != "." {
				importPath = modPath + "/" + filepath.ToSlash(path)
			}
			pkgs = append(pkgs, importPath)
		}
		return nil
	})
	return pkgs, err
}

// hasMainPackage returns true if the directory at path contains at least one
// non-test .go file whose package declaration is "main".
func hasMainPackage(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if packageNameFromFile(filepath.Join(dir, name)) == "main" {
			return true
		}
	}
	return false
}

// packageNameFromFile reads the package declaration from a Go source file.
// It scans only until the package line is found, avoiding parsing the whole file.
func packageNameFromFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip blank lines, comments, and build constraints
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "/*") {
			continue
		}
		if strings.HasPrefix(line, "package ") {
			// Extract the package name (first token after "package")
			name := strings.TrimPrefix(line, "package ")
			// Handle "package main // comment" or "package main;"
			if idx := strings.IndexAny(name, " \t;/"); idx != -1 {
				name = name[:idx]
			}
			return strings.TrimSpace(name)
		}
		// If we hit a non-comment, non-blank, non-package line, the file
		// is malformed or we've gone past the package declaration area.
		// In practice this shouldn't happen for valid Go files.
		break
	}
	return ""
}
