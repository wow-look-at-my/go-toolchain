// Package memlimit injects a small, stdlib-only startup guard into every main
// package go-toolchain builds. The guard reads the container's cgroup memory
// limit and sets runtime/debug.SetMemoryLimit (GOMEMLIMIT) to a fraction of it,
// so the Go garbage collector stays under the cgroup ceiling instead of
// allocating until the kernel OOM-kills the process.
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

// GuardFileName is the name of the generated file written into each main
// package. The _gen suffix marks it as generated; it carries a build constraint
// (go1.19) and is stdlib-only, so it compiles on every supported platform.
const GuardFileName = "gomemlimit_gen.go"

// guardSource is the exact content written into each main package.
//
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

// InjectAll discovers every main package under the current module and injects
// the guard into each. It returns the directories that were created or updated.
// When there is no module (no go.mod) it is a no-op.
func InjectAll() ([]string, error) {
	pkgs, err := gomod.FindMainPackages()
	if err != nil {
		return nil, err
	}
	modPath := gomod.ReadModulePath()

	var changed []string
	for _, importPath := range pkgs {
		dir := "."
		if modPath != "" && importPath != modPath {
			dir = strings.TrimPrefix(importPath, modPath+"/")
		}
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
