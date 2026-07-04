// Package gomod provides filesystem-based Go module utilities that replace
// go list invocations with direct parsing of go.mod and source files.
package gomod

import (
	"bufio"
	"go/build"
	"go/parser"
	"go/token"
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
		// Read the cheap "package" clause FIRST. The vast majority of files in
		// a module are not "package main", and for those the constraint check
		// is irrelevant: a build-excluded non-main file was never going to be
		// counted as a main package anyway. Doing this first avoids a
		// build-constraint parse (build.Default.MatchFile) on every .go file
		// in the module tree on every build invocation.
		if packageNameFromFile(filepath.Join(dir, name)) != "main" {
			continue
		}
		// This file declares "package main". Only now pay for the constraint
		// check: honor build constraints so a file excluded from the build for
		// the current context (notably "//go:build ignore", but also a
		// GOOS/GOARCH mismatch) does not contribute a "package main". This
		// stops generator idioms such as "//go:build ignore" + "package main"
		// (run via `go run`) from being misidentified as the directory's main
		// package, while a legitimately-constrained main (e.g. "//go:build
		// linux") is still discovered under the matching context.
		if !fileMatchesBuild(dir, name) {
			continue
		}
		return true
	}
	return false
}

// fileMatchesBuild reports whether the named .go file in dir is part of the
// build for the current build context, honoring "//go:build" / "// +build"
// constraints (including the never-satisfied "ignore" tag) and the
// GOOS/GOARCH-encoding filename convention. It uses go/build's own matcher so
// the result matches what `go build` would actually compile. A file that
// go/build cannot classify is treated as included, preserving the previous
// build-tag-blind behavior for anything MatchFile can't decide.
func fileMatchesBuild(dir, name string) bool {
	matched, err := matchFile(dir, name)
	if err != nil {
		return true
	}
	return matched
}

// matchFile is the build-constraint matcher used by fileMatchesBuild. It is a
// package-level variable (defaulting to go/build's own matcher) so tests can
// observe exactly which files the constraint check is consulted for — it must
// only ever run on "package main" candidates, never on every .go file.
var matchFile = build.Default.MatchFile

// packageNameFromFile reads the package declaration from a Go source file
// using go/parser in PackageClauseOnly mode, which stops after the package
// clause instead of parsing the whole file. The real parser handles every
// comment form the old hand-rolled line scanner tripped over — most notably a
// multi-line /* */ license header (the k8s-style boilerplate), whose
// continuation lines matched none of the scanner's prefixes and made it bail
// before ever reaching the package clause, silently hiding a main package
// from the build. Returns "" when the file has no parseable package clause.
func packageNameFromFile(path string) string {
	// A partial AST may still carry the package name even when ParseFile
	// reports an error (e.g. junk after the clause); use it if present.
	f, _ := parser.ParseFile(token.NewFileSet(), path, nil, parser.PackageClauseOnly)
	if f == nil || f.Name == nil {
		return ""
	}
	return f.Name.Name
}
