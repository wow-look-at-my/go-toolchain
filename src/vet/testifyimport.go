package vet

import (
	"bytes"
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

// importRewrite records one fork->upstream import path change, for printing.
type importRewrite struct{ old, new string }

// FixTestifyImports scans all Go files for imports of the in-house
// wow-look-at-my/testify fork and routes each offending file through ed. A
// fix-mode editor rewrites the import to upstream stretchr/testify and resyncs
// the module graph (go mod tidy, plus go mod vendor when the repo vendors its
// dependencies); a check-mode (CI) editor records a violation instead, so a
// tree still on the fork fails CI rather than passing green. (The fork is being
// removed, so an unmigrated import is a latent unresolvable-module break.)
//
// Returns whether any file was written (only possible with a fix-mode editor).
func FixTestifyImports(ed Editor) (bool, error) {
	var anyWrote bool

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
		newSrc, changes, err := renderTestifyImports(p)
		if err != nil {
			return err
		}
		if len(changes) == 0 {
			return nil
		}
		wrote, err := ed.Require(p, newSrc, "imports the removed github.com/wow-look-at-my/testify fork; migrate to github.com/stretchr/testify")
		if err != nil {
			return err
		}
		if wrote {
			anyWrote = true
			for _, ch := range changes {
				printTestifyFix(p, ch.old, ch.new)
			}
		}
		return nil
	})

	if err != nil {
		return false, err
	}

	// Sync the module graph only after we actually rewrote files, so upstream
	// testify is the required module and any vendor tree stays consistent.
	if anyWrote {
		if err := syncModuleGraph(); err != nil {
			return anyWrote, err
		}
	}

	return anyWrote, nil
}

// renderTestifyImports parses filename and returns its source with every
// wow-look-at-my/testify fork import rewritten to upstream stretchr/testify
// (sub-package path preserved), along with the list of rewrites. It performs no
// write; a file with no fork import returns (nil, nil, nil).
func renderTestifyImports(filename string) ([]byte, []importRewrite, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		// Unparseable file: the type-check/go vet pass reports the syntax error
		// with a proper location; don't surface it here as an import problem.
		return nil, nil, nil
	}

	var changes []importRewrite
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if strings.HasPrefix(path, forkTestify) {
			newPath := upstreamTestify + strings.TrimPrefix(path, forkTestify)
			imp.Path.Value = `"` + newPath + `"`
			changes = append(changes, importRewrite{old: path, new: newPath})
		}
	}

	if len(changes) == 0 {
		return nil, nil, nil
	}

	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, f); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), changes, nil
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
