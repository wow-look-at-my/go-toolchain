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

// IsNestedModule reports whether dir (other than ".") holds its own go.mod, marking it a
// separate module. Filesystem walkers must skip these dirs: their files are not part of
// this module's build and must not be rewritten by its fixers.
func IsNestedModule(dir string) bool {
	if dir == "." {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "go.mod"))
	return err == nil
}

// MemLimitGuardFileName names the transient GOMEMLIMIT guard file; discovery skips it by name so a host-only main dir does not leak into other targets.
const MemLimitGuardFileName = "gomemlimit_gen.go"

// FindMainPackages returns the import paths of all main packages, found by walking the
// module tree. Build constraints are evaluated against the host context.
func FindMainPackages() ([]string, error) {
	return findMainPackages(matchFile)
}

// FindMainPackagesForTarget is FindMainPackages evaluated under an explicit GOOS/GOARCH
// context instead of the host, matching what `go build` would compile for that target.
func FindMainPackagesForTarget(goos, goarch string) ([]string, error) {
	ctx := build.Default
	ctx.GOOS = goos
	ctx.GOARCH = goarch
	return findMainPackages(ctx.MatchFile)
}

// findMainPackages is the shared walk behind FindMainPackages and
// FindMainPackagesForTarget; match evaluates build constraints for the
// desired context.
func findMainPackages(match func(dir, name string) (bool, error)) ([]string, error) {
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
		// Skip nested modules: their main packages belong to their own module
		// and are not buildable as import paths of this one.
		if IsNestedModule(path) {
			return filepath.SkipDir
		}

		// Check if this directory has a non-test .go file with package main
		if hasMainPackageMatch(path, match) {
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

// hasMainPackage reports whether dir has a non-test "package main" file, under the host build context.
func hasMainPackage(dir string) bool {
	return hasMainPackageMatch(dir, matchFile)
}

// hasMainPackageMatch is hasMainPackage with an explicit build-constraint
// matcher (see FindMainPackagesForTarget).
func hasMainPackageMatch(dir string, match func(dir, name string) (bool, error)) bool {
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
		// Skip the transient memlimit guard: counting it would leak host-only main dirs
		// into other targets' discovery.
		if name == MemLimitGuardFileName {
			continue
		}
		// Check the package name first: most files are not "package main", so this skips
		// the build-constraint parse on every .go file in the tree.
		if packageNameFromFile(filepath.Join(dir, name)) != "main" {
			continue
		}
		// Now check build constraints, so a "//go:build ignore" generator or a
		// GOOS/GOARCH mismatch is not misidentified as the directory's main package.
		if matched, err := match(dir, name); err == nil && !matched {
			continue
		}
		return true
	}
	return false
}

// matchFile is the host build-constraint matcher, a var so tests can observe its calls; errors fail open (included).
var matchFile = build.Default.MatchFile

// packageNameFromFile reads a Go file's package name using go/parser in PackageClauseOnly
// mode, which stops after the package clause instead of parsing the whole file. This
// correctly handles a multi-line comment before the clause, unlike a naive line scanner.
// Returns "" when the file has no parseable package clause.
func packageNameFromFile(path string) string {
	// Use the partial AST's package name even if ParseFile also returned an error.
	f, _ := parser.ParseFile(token.NewFileSet(), path, nil, parser.PackageClauseOnly)
	if f == nil || f.Name == nil {
		return ""
	}
	return f.Name.Name
}
