package build

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wow-look-at-my/go-containers/sortedmap"
	"github.com/wow-look-at-my/go-toolchain/src/gomod"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

type Target struct {
	ImportPath string
	OutputName string
}

// findMainPackages walks the filesystem to discover all main packages in the module.
func findMainPackages() ([]string, error) {
	return gomod.FindMainPackages()
}

// binaryNameFromImportPath derives a binary name from a package's import path
// and its module name. When the package is at or one level below the module root
// (e.g., module or module/src), the binary is named after the module. When deeper
// (e.g., module/cmd/foo), the binary is named after the leaf directory.
func binaryNameFromImportPath(pkg, moduleName string) string {
	if pkg == moduleName {
		// Package is at module root — use module name
		return filepath.Base(moduleName)
	}
	rel := strings.TrimPrefix(pkg, moduleName+"/")
	if rel == pkg {
		// Package doesn't start with module prefix — just use basename
		return filepath.Base(pkg)
	}
	if !strings.Contains(rel, "/") {
		// Single level below module root (e.g., "src") — use module name
		return filepath.Base(moduleName)
	}
	// Deeper path (e.g., "cmd/foo") — use leaf directory
	return filepath.Base(pkg)
}

// ResolveBuildTargets determines what to build and what to name the binaries.
// Walks the filesystem to find all main packages in the module. If no main
// packages exist (library-only project), falls back to all packages found by
// walking the filesystem.
// Binary names are always auto-derived from the package/directory name.
func ResolveBuildTargets(r runner.CommandRunner) ([]Target, error) {
	// Get module name for smart binary naming
	moduleName := gomod.ReadModulePath()

	// Find all main packages in the module
	pkgs, err := findMainPackages()
	if err != nil {
		return nil, err
	}

	if len(pkgs) > 0 {
		return nameTargets(pkgs, moduleName)
	}

	// Library-only project: walk filesystem to find all packages
	allPkgs, err := findAllPackagesByDir(moduleName)
	if err != nil {
		return nil, err
	}
	targets := make([]Target, len(allPkgs))
	for i, pkg := range allPkgs {
		targets[i] = Target{ImportPath: pkg, OutputName: filepath.Base(pkg)}
	}
	return targets, nil
}

// ResolveBuildTargetsForTarget resolves the main packages visible under an
// explicit cross-compile target's build context (GOOS/GOARCH), so a main
// package guarded e.g. "//go:build js && wasm" is discovered for js/wasm
// targets while a "//go:build linux" main is discovered for linux targets —
// regardless of the host platform. Unlike ResolveBuildTargets there is no
// library-only fallback: an empty result means this target has no main
// packages to build (the caller decides whether that is a skip or an error).
func ResolveBuildTargetsForTarget(goos, goarch string) ([]Target, error) {
	moduleName := gomod.ReadModulePath()
	pkgs, err := gomod.FindMainPackagesForTarget(goos, goarch)
	if err != nil {
		return nil, err
	}
	return nameTargets(pkgs, moduleName)
}

// nameTargets assigns each main package the name its binary is written under.
//
// The module-derived name goes only to a package that is alone in wanting it.
// Two mains one level below the module root -- `<mod>/cli` and
// `<mod>/todo_driver`, say -- both derive the MODULE's name, and a build that
// keeps whichever it saw first ships missing a binary while reporting success.
// A contested name falls back to the package's own leaf directory, which is
// unique among the packages of one module.
//
// A name still contested after that cannot happen from one module's packages,
// so it is a hard error rather than a quiet loss.
func nameTargets(pkgs []string, moduleName string) ([]Target, error) {
	wanted := map[string]int{}
	for _, pkg := range pkgs {
		wanted[binaryNameFromImportPath(pkg, moduleName)]++
	}

	byName := sortedmap.New[string, Target]()
	for _, pkg := range pkgs {
		name := binaryNameFromImportPath(pkg, moduleName)
		if wanted[name] > 1 {
			name = filepath.Base(pkg)
		}
		if prev, taken := byName.Get(name); taken {
			return nil, fmt.Errorf("main packages %s and %s both build to %q: rename one of their directories",
				prev.ImportPath, pkg, name)
		}
		byName.Put(name, Target{ImportPath: pkg, OutputName: name})
	}
	return slices.Collect(byName.Values()), nil
}

// findAllPackagesByDir walks the filesystem from the current directory to find
// all directories containing .go files, returning them as import paths.
func findAllPackagesByDir(moduleName string) ([]string, error) {
	var pkgs []string
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		// Skip hidden dirs, testdata, vendor, and nested modules (their
		// packages belong to a different module and are not import paths
		// of this one).
		base := d.Name()
		if base != "." && (strings.HasPrefix(base, ".") || base == "testdata" || base == "vendor" || gomod.IsNestedModule(path)) {
			return filepath.SkipDir
		}
		// Check if dir contains any .go files
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
				importPath := moduleName
				if path != "." {
					importPath = moduleName + "/" + filepath.ToSlash(path)
				}
				pkgs = append(pkgs, importPath)
				break
			}
		}
		return nil
	})
	return pkgs, err
}
