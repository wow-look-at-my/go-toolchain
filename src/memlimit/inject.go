// Package memlimit injects a small, stdlib-only startup guard into every main
// package go-toolchain builds. The guard reads the container's cgroup memory
// limit and sets runtime/debug.SetMemoryLimit (GOMEMLIMIT) to a fraction of it,
// so the Go garbage collector stays under the cgroup ceiling instead of
// allocating until the kernel OOM-kills the process.
//
// The guard is a transient build artifact, not a committed source file:
// InjectAll writes it into each main package immediately before the build
// compiles them, and CleanupAll removes it again as soon as the build is done.
// This keeps the generated file out of the working tree, so it never shows up as
// an uncommitted change or trips go-toolchain's dirty-tree check in CI. The cmd
// layer additionally lists the guard in the repo's clone-local .git/info/exclude
// at inject time (see cmd's ensureGuardExcluded), so the go command's own VCS
// stamping — a `git status` taken while the guard exists — never sees it as an
// untracked file and built binaries don't stamp "+dirty" on clean checkouts.
//
// The shipped guard is testdata/guard.go — that file is the editable source of
// truth and is embedded verbatim into consumers (it is kept under testdata so
// the go tool and go-toolchain's own main-package discovery never try to build
// it in place). The behavior of the embedded file is verified end to end in
// guard_test.go.
package memlimit

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/gomod"
)

// GuardFileName names the generated guard; main-package discovery skips it so a stale copy is never misread as a real main package.
const GuardFileName = gomod.MemLimitGuardFileName

//go:embed testdata/guard.go
var guardSource string

// Inject writes the GOMEMLIMIT guard into dir if it is missing or out of date.
// It reports whether the file was created or updated. Injection is idempotent:
// when the file already matches the current guard, nothing is written and it
// returns (false, nil).
func Inject(dir string) (bool, error) {
	target := filepath.Join(dir, GuardFileName)

	existing, err := os.ReadFile(target)
	switch {
	case err == nil:
		if string(existing) == guardSource {
			return false, nil
		}
	case !os.IsNotExist(err):
		return false, err
	}

	if err := os.WriteFile(target, []byte(guardSource), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// mainPackageDirs returns the directory, relative to the module root, of every
// main package under the current module: "." for the root package, "cmd/tool"
// for a nested package, and so on. It is the shared discovery used by InjectAll
// and CleanupAll. When there is no module (no go.mod) it returns nil.
func mainPackageDirs() ([]string, error) {
	pkgs, err := gomod.FindMainPackages()
	if err != nil {
		return nil, err
	}
	modPath := gomod.ReadModulePath()

	dirs := make([]string, 0, len(pkgs))
	for _, importPath := range pkgs {
		dir := "."
		if modPath != "" && importPath != modPath {
			dir = strings.TrimPrefix(importPath, modPath+"/")
		}
		dirs = append(dirs, dir)
	}
	return dirs, nil
}

// InjectAll discovers every main package under the current module and injects
// the guard into each. It returns the directories that were created or updated.
// When there is no module (no go.mod) it is a no-op.
func InjectAll() ([]string, error) {
	dirs, err := mainPackageDirs()
	if err != nil {
		return nil, err
	}

	var changed []string
	for _, dir := range dirs {
		updated, err := Inject(dir)
		if err != nil {
			return changed, err
		}
		if updated {
			changed = append(changed, dir)
		}
	}
	return changed, nil
}

// CleanupAll removes the injected guard from every main package under the current module, returning the
// directories a guard was removed from. It is InjectAll's inverse, run immediately after the build so the
// generated file never lingers or shows up as an uncommitted change. A guard already absent is not an error;
// with no module it is a no-op.
func CleanupAll() ([]string, error) {
	dirs, err := mainPackageDirs()
	if err != nil {
		return nil, err
	}

	var removed []string
	for _, dir := range dirs {
		target := filepath.Join(dir, GuardFileName)
		switch err := os.Remove(target); {
		case err == nil:
			removed = append(removed, dir)
		case os.IsNotExist(err):
			// Nothing to clean up in this package.
		default:
			return removed, err
		}
	}
	return removed, nil
}
