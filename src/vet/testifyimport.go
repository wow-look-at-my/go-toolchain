package vet

import (
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	ansi "github.com/wow-look-at-my/ansi-writer"
)

const (
	// forkTestify is the in-house fork that loosened numeric equality. We are
	// migrating off it back to upstream; the cast-inserting analyzer
	// (testifycast) preserves the loose-equality behavior with explicit casts.
	forkTestify = "github.com/wow-look-at-my/testify/"
	// upstreamTestify is the canonical, widely-audited module.
	upstreamTestify = "github.com/stretchr/testify/"
)

// FixTestifyImports scans all Go files and replaces the in-house
// wow-look-at-my/testify fork imports with upstream stretchr/testify. After
// rewriting it syncs the module graph (go mod tidy, plus go mod vendor when the
// repo vendors its dependencies) so go.mod, go.sum and vendor/modules.txt all
// agree. Returns true if any files were modified.
func FixTestifyImports() (bool, error) {
	var anyFixed bool

	err := filepath.WalkDir(".", func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "vendor" || d.Name() == ".git" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, ".go") {
			fixed, err := fixFileTestifyImports(p)
			if err != nil {
				return err
			}
			if fixed {
				anyFixed = true
			}
		}
		return nil
	})

	if err != nil {
		return false, err
	}

	// Sync the module graph after import changes so upstream testify is the
	// required module and any vendor tree is consistent.
	if anyFixed {
		if err := syncModuleGraph(); err != nil {
			return anyFixed, err
		}
	}

	return anyFixed, nil
}

// fixFileTestifyImports fixes testify imports in a single file.
// Returns true if the file was modified.
func fixFileTestifyImports(filename string) (bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		return false, err
	}

	var modified bool
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if strings.HasPrefix(path, forkTestify) {
			// Replace the fork with upstream, preserving the sub-package path.
			newPath := upstreamTestify + strings.TrimPrefix(path, forkTestify)
			imp.Path.Value = `"` + newPath + `"`
			modified = true

			printTestifyFix(filename, path, newPath)
		}
	}

	if !modified {
		return false, nil
	}

	// Write back
	out, err := os.Create(filename)
	if err != nil {
		return false, err
	}
	defer out.Close()

	if err := printer.Fprint(out, fset, f); err != nil {
		return false, err
	}

	return true, nil
}

// syncModuleGraph runs go mod tidy and, when the repo vendors its dependencies,
// go mod vendor — leaving go.mod, go.sum and vendor/modules.txt consistent.
func syncModuleGraph() error {
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Stdout = os.Stdout
	tidy.Stderr = os.Stderr
	if err := tidy.Run(); err != nil {
		return err
	}
	return syncVendorIfPresent()
}

// syncVendorIfPresent rebuilds the vendor tree when vendor/modules.txt exists so
// that a vendored repo (e.g. containerd) stays buildable with -mod=vendor after
// imports change. It is a no-op for repos that don't vendor.
func syncVendorIfPresent() error {
	if _, err := os.Stat(filepath.Join("vendor", "modules.txt")); err != nil {
		return nil // not a vendored module
	}
	vendor := exec.Command("go", "mod", "vendor")
	vendor.Stdout = os.Stdout
	vendor.Stderr = os.Stderr
	return vendor.Run()
}

func printTestifyFix(filename, oldImport, newImport string) {
	yellow := ansi.Concat(ansi.Yellow.FG, "fixed:", ansi.Reset)
	grey := ansi.Concat(ansi.BrightBlack.FG, filename, ansi.Reset)
	red := ansi.Concat(ansi.Red.FG, oldImport, ansi.Reset)
	green := ansi.Concat(ansi.Green.FG, newImport, ansi.Reset)
	println(yellow, grey, red, "→", green)
}
