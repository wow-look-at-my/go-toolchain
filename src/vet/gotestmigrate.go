package vet

import (
	"go/ast"
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
	gotestAssert    = "gotest.tools/v3/assert"
	gotestAssertCmp = "gotest.tools/v3/assert/cmp"
	testifyRequire  = "github.com/wow-look-at-my/testify/require"
	testifyAssert   = "github.com/wow-look-at-my/testify/assert"
)

// gotestFuncRenames maps gotest.tools function names to their testify/require equivalents.
var gotestFuncRenames = map[string]string{
	"NilError":  "NoError",
	"Error":     "EqualError",
	"DeepEqual": "Equal",
	// These map to the same name — listed for clarity
	"ErrorContains": "ErrorContains",
	"ErrorIs":       "ErrorIs",
	"Equal":         "Equal",
}

// MigrateGotestTools scans all Go files and migrates gotest.tools/v3/assert
// imports to github.com/wow-look-at-my/testify/require. Returns true if any
// files were modified.
func MigrateGotestTools() (bool, error) {
	var anyFixed bool

	err := filepath.WalkDir(".", func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "vendor" || d.Name() == ".git" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, ".go") {
			fixed, err := migrateFileGotestTools(p)
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

// migrateFileGotestTools migrates gotest.tools imports in a single file.
// Returns true if the file was modified.
func migrateFileGotestTools(filename string) (bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		return false, err
	}

	var modified bool
	var hasAssertImport bool  // tracks if gotest.tools/v3/assert was present
	var hasCmpImport bool     // tracks if gotest.tools/v3/assert/cmp was present
	var hasCheckCalls bool    // tracks if assert.Check calls exist
	var needAssertImport bool // whether to add testify/assert import

	// Phase 1: Rewrite imports
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)

		switch path {
		case gotestAssert:
			imp.Path.Value = `"` + testifyRequire + `"`
			// If it had an explicit alias "assert", change to "require"
			if imp.Name != nil && imp.Name.Name == "assert" {
				imp.Name.Name = "require"
			}
			modified = true
			hasAssertImport = true
			printGotestFix(filename, path, testifyRequire)

		case gotestAssertCmp:
			hasCmpImport = true
			// We'll remove this import after scanning for usages
		}
	}

	if !hasAssertImport {
		return false, nil
	}

	// Phase 2: Walk AST to rename selectors and function names
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}

		if ident.Name != "assert" {
			return true
		}

		funcName := sel.Sel.Name

		// Special case: Check is non-fatal → maps to testify assert (not require)
		if funcName == "Check" {
			hasCheckCalls = true
			needAssertImport = true
			// Keep selector as "assert" — it will refer to testify/assert
			sel.Sel.Name = "True"
			modified = true
			return true
		}

		// Rename the package selector from assert → require
		ident.Name = "require"

		// Apply function name renames
		if newName, ok := gotestFuncRenames[funcName]; ok {
			sel.Sel.Name = newName
		}

		modified = true
		return true
	})

	if !modified {
		return false, nil
	}

	// Phase 3: Remove cmp import if present
	if hasCmpImport {
		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, `"`) == gotestAssertCmp {
				removeImport(f, imp)
				break
			}
		}
		printGotestFix(filename, gotestAssertCmp, "(removed)")
	}

	// Phase 4: Add testify/assert import if Check calls were found
	if needAssertImport && hasCheckCalls {
		addImport(f, testifyAssert)
		printGotestFix(filename, "assert.Check", "assert.True (non-fatal)")
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

// addImport adds an import path to the file's first import declaration.
func addImport(f *ast.File, path string) {
	newSpec := &ast.ImportSpec{
		Path: &ast.BasicLit{
			Kind:  token.STRING,
			Value: `"` + path + `"`,
		},
	}

	// Add to the first import group
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.IMPORT {
			continue
		}
		genDecl.Specs = append(genDecl.Specs, newSpec)
		f.Imports = append(f.Imports, newSpec)
		return
	}

	// No import decl exists — create one
	genDecl := &ast.GenDecl{
		Tok:   token.IMPORT,
		Specs: []ast.Spec{newSpec},
	}
	f.Decls = append([]ast.Decl{genDecl}, f.Decls...)
	f.Imports = append(f.Imports, newSpec)
}

func printGotestFix(filename, oldRef, newRef string) {
	yellow := ansi.Concat(ansi.Yellow.FG, "fixed:", ansi.Reset)
	grey := ansi.Concat(ansi.BrightBlack.FG, filename, ansi.Reset)
	red := ansi.Concat(ansi.Red.FG, oldRef, ansi.Reset)
	green := ansi.Concat(ansi.Green.FG, newRef, ansi.Reset)
	println(yellow, grey, red, "→", green)
}
