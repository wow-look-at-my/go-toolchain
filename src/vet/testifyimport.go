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
	brokenTestify  = "github.com/stretchr/testify/"
	correctTestify = "github.com/wow-look-at-my/testify/"
)

// FixTestifyImports scans all Go files and replaces stretchr/testify imports
// with wow-look-at-my/testify. Returns true if any files were modified.
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

	// Run go mod tidy to update dependencies after import changes
	if anyFixed {
		cmd := exec.Command("go", "mod", "tidy")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
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
		if strings.HasPrefix(path, brokenTestify) {
			// Replace with correct import
			newPath := correctTestify + strings.TrimPrefix(path, brokenTestify)
			imp.Path.Value = `"` + newPath + `"`
			modified = true

			// Print fix message
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

func printTestifyFix(filename, oldImport, newImport string) {
	yellow := ansi.Concat(ansi.Yellow.FG, "fixed:", ansi.Reset)
	grey := ansi.Concat(ansi.BrightBlack.FG, filename, ansi.Reset)
	red := ansi.Concat(ansi.Red.FG, oldImport, ansi.Reset)
	green := ansi.Concat(ansi.Green.FG, newImport, ansi.Reset)
	println(yellow, grey, red, "→", green)
}
