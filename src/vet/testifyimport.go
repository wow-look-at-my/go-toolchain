package vet

import (
	"fmt"
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

// FixTestifyImports scans all Go files for imports of the in-house
// wow-look-at-my/testify fork.
//
// In fix mode (fix == true, the local default) it replaces every fork import
// with upstream stretchr/testify, then syncs the module graph (go mod tidy,
// plus go mod vendor when the repo vendors its dependencies) so go.mod, go.sum
// and vendor/modules.txt all agree.
//
// In check mode (fix == false, the CI path) it writes nothing and instead
// returns a hard error listing every file that still imports the fork —
// mirroring RunGofmt's check mode — so a non-canonical tree fails CI instead of
// passing green on the fork. This is the enforcement that was missing: the fork
// is being removed, so an unmigrated import is a latent unresolvable-module
// break, and CI must reject it rather than silently accept it.
//
// Returns true if any files were modified (only possible in fix mode).
func FixTestifyImports(fix bool) (bool, error) {
	var anyFixed bool
	var offending []string

	err := filepath.WalkDir(".", func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "vendor" || d.Name() == ".git" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}
		if fix {
			fixed, err := fixFileTestifyImports(p)
			if err != nil {
				return err
			}
			if fixed {
				anyFixed = true
			}
			return nil
		}
		// Check mode: detect only, never write.
		hasFork, err := fileImportsTestifyFork(p)
		if err != nil {
			return err
		}
		if hasFork {
			offending = append(offending, p)
		}
		return nil
	})

	if err != nil {
		return false, err
	}

	if !fix {
		if len(offending) > 0 {
			return false, forkImportError(offending)
		}
		return false, nil
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

// fileImportsTestifyFork reports whether filename imports the in-house
// wow-look-at-my/testify fork. It parses imports only — no rewrite, no write —
// so it is safe to run on CI in check mode.
func fileImportsTestifyFork(filename string) (bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, nil, parser.ImportsOnly)
	if err != nil {
		return false, err
	}
	for _, imp := range f.Imports {
		if strings.HasPrefix(strings.Trim(imp.Path.Value, `"`), forkTestify) {
			return true, nil
		}
	}
	return false, nil
}

// forkImportError builds the check-mode failure listing every file that still
// imports the fork, with the exact local remedy.
func forkImportError(files []string) error {
	var sb strings.Builder
	sb.WriteString("testify: the following files import the removed " +
		"github.com/wow-look-at-my/testify fork (run `go-toolchain` locally to " +
		"migrate to github.com/stretchr/testify):\n")
	for _, f := range files {
		sb.WriteString("  " + f + "\n")
	}
	return fmt.Errorf("%s", strings.TrimRight(sb.String(), "\n"))
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
