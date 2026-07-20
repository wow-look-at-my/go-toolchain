package test

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/gomod"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// readModulePath reads the module path from go.mod in the current directory.
func readModulePath() string {
	return gomod.ReadModulePath()
}

// listTestPackages returns the import paths of packages that contain test files,
// excluding packages where all non-test .go files are generated code (e.g. sqlc).
// It walks the filesystem directly instead of shelling out to `go list`, which
// is significantly faster.
// On any error it returns nil, signaling the caller to fall back to "./...".
func listTestPackages(_ runner.CommandRunner) []string {
	modPath := readModulePath()
	if modPath == "" {
		return nil
	}
	var pkgs []string
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable dirs
		}
		if !d.IsDir() {
			return nil
		}
		// Skip hidden dirs, common non-source dirs, and nested modules
		// (their packages belong to a different module and are not import
		// paths of this one).
		name := d.Name()
		if name != "." && (strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" || gomod.IsNestedModule(path)) {
			return filepath.SkipDir
		}
		// Skip packages where all non-test .go files are generated code
		if isGeneratedPackage(path) {
			return nil
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), "_test.go") {
				rel := filepath.ToSlash(path)
				if rel == "." {
					pkgs = append(pkgs, modPath)
				} else {
					pkgs = append(pkgs, modPath+"/"+rel)
				}
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil
	}
	return pkgs
}
