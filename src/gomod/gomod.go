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

// IsNestedModule reports whether dir is the root of ANOTHER Go module nested
// inside the tree being walked — i.e. a directory (other than the walk root
// ".") containing its own go.mod. Filesystem walkers (gofmt, the import
// fixers, the file-length check, main-package discovery) must skip such
// directories: their files belong to a different module — e.g. the vendored
// src/compat/go-isatty — are not covered by "./...", and must not be
// rewritten by the outer module's fixers.
func IsNestedModule(dir string) bool {
	if dir == "." {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "go.mod"))
	return err == nil
}

// MemLimitGuardFileName is the filename of the transient GOMEMLIMIT guard
// that src/memlimit injects into main packages during builds (memlimit's
// GuardFileName is defined from this constant — gomod cannot import memlimit,
// which imports gomod). Main-package discovery skips the file by name: the
// guard is an UNCONSTRAINED "package main" file injected into dirs that are
// main packages under the HOST context, so during per-target discovery (e.g.
// GOOS=js) a guard — freshly injected, or stale from an interrupted build —
// would make a host-only main dir (say //go:build linux) look like a main
// package for every other target, whose build of that dir would then fail
// (the guard alone has no main()). The guard never makes a dir a main
// package by itself, so skipping it cannot hide a real main.
const MemLimitGuardFileName = "gomemlimit_gen.go"

// FindMainPackages walks the filesystem from the current directory to find
// all directories containing non-test .go files with "package main" declarations,
// returning their import paths (module path + relative directory). Build
// constraints are evaluated against the HOST context (build.Default).
func FindMainPackages() ([]string, error) {
	return findMainPackages(matchFile)
}

// FindMainPackagesForTarget is FindMainPackages under an explicit target
// build context: constraints are evaluated with GOOS/GOARCH set to the given
// target (derived from build.Default, so local tags and toolchain defaults
// are otherwise preserved). A main package guarded e.g. "//go:build js &&
// wasm" is discovered for GOOS=js GOARCH=wasm and invisible to native
// targets, matching what `go build` would compile for that target.
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

// hasMainPackage returns true if the directory at path contains at least one
// non-test .go file whose package declaration is "main", under the host
// build context.
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
		// Never count the transient memlimit guard: it is an unconstrained
		// "package main" file injected only into dirs that already have a
		// real main under the host context, so honoring it here would leak
		// host main dirs into other targets' discovery (see
		// MemLimitGuardFileName).
		if name == MemLimitGuardFileName {
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
		// the desired context (notably "//go:build ignore", but also a
		// GOOS/GOARCH mismatch) does not contribute a "package main". This
		// stops generator idioms such as "//go:build ignore" + "package main"
		// (run via `go run`) from being misidentified as the directory's main
		// package, while a legitimately-constrained main (e.g. "//go:build
		// linux") is still discovered under the matching context.
		if matched, err := match(dir, name); err == nil && !matched {
			continue
		}
		return true
	}
	return false
}

// matchFile is the host-context build-constraint matcher (defaulting to
// go/build's build.Default.MatchFile). It is a package-level variable so
// tests can observe exactly which files the constraint check is consulted
// for — it must only ever run on "package main" candidates, never on every
// .go file. Match errors fail open: a file go/build cannot classify is
// treated as included, preserving the previous build-tag-blind behavior.
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
