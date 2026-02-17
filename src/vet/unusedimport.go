package vet

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

// FixUnusedImports scans all Go files matching the pattern and removes unused imports.
// Returns list of fixed files.
func FixUnusedImports(pattern string) ([]string, error) {
	// Find all .go files
	var files []string
	if pattern == "./..." {
		err := filepath.WalkDir(".", func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == "vendor" || d.Name() == ".git") {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(p, ".go") {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		// For other patterns, use filepath.Glob
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			if strings.HasSuffix(m, ".go") {
				files = append(files, m)
			}
		}
	}

	var fixed []string
	for _, file := range files {
		wasFixed, err := fixFileUnusedImports(file)
		if err != nil {
			return fixed, fmt.Errorf("fixing %s: %w", file, err)
		}
		if wasFixed {
			fixed = append(fixed, file)
		}
	}

	return fixed, nil
}

// fixFileUnusedImports fixes unused imports in a single file.
// Returns true if the file was modified.
func fixFileUnusedImports(filename string) (bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		return false, err
	}

	// Collect imports
	imports := make(map[string]*ast.ImportSpec)
	for _, imp := range f.Imports {
		name := importName(imp)
		if name == "_" || name == "." {
			continue
		}
		imports[name] = imp
	}

	if len(imports) == 0 {
		return false, nil
	}

	// Find used imports
	used := make(map[string]bool)
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if ident, ok := sel.X.(*ast.Ident); ok {
				used[ident.Name] = true
			}
		}
		return true
	})

	// Find unused
	var unused []*ast.ImportSpec
	for name, imp := range imports {
		if !used[name] {
			unused = append(unused, imp)
		}
	}

	if len(unused) == 0 {
		return false, nil
	}

	// Remove unused imports from AST
	for _, imp := range unused {
		removeImport(f, imp)
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

// removeImport removes an import from the file's AST.
func removeImport(f *ast.File, imp *ast.ImportSpec) {
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.IMPORT {
			continue
		}

		for i, spec := range gd.Specs {
			if spec == imp {
				gd.Specs = append(gd.Specs[:i], gd.Specs[i+1:]...)
				break
			}
		}
	}

	// Also remove from f.Imports
	for i, spec := range f.Imports {
		if spec == imp {
			f.Imports = append(f.Imports[:i], f.Imports[i+1:]...)
			break
		}
	}
}

// importName returns the local name for an import.
func importName(imp *ast.ImportSpec) string {
	if imp.Name != nil {
		return imp.Name.Name
	}
	p := strings.Trim(imp.Path.Value, `"`)
	return path.Base(p)
}

// FindUnusedImportFixes analyzes loaded packages and returns AST-based fixes for unused imports.
func FindUnusedImportFixes(pkgs []*packages.Package) []*ASTFixes {
	var result []*ASTFixes

	for _, pkg := range pkgs {
		for i, f := range pkg.Syntax {
			if i >= len(pkg.CompiledGoFiles) {
				continue
			}
			if fixes := findFileUnusedImportFixes(pkg.Fset, f); fixes != nil {
				result = append(result, fixes)
			}
		}
	}

	return result
}

func findFileUnusedImportFixes(fset *token.FileSet, f *ast.File) *ASTFixes {
	// Collect imports
	imports := make(map[string]*ast.ImportSpec)
	for _, imp := range f.Imports {
		name := importName(imp)
		if name == "_" || name == "." {
			continue
		}
		imports[name] = imp
	}

	if len(imports) == 0 {
		return nil
	}

	// Find used imports
	used := make(map[string]bool)
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if ident, ok := sel.X.(*ast.Ident); ok {
				used[ident.Name] = true
			}
		}
		return true
	})

	// Generate fixes for unused imports (deletion = nil NewNode)
	var fixes []ASTFix
	for name, imp := range imports {
		if used[name] {
			continue
		}
		fixes = append(fixes, ASTFix{
			OldNode: imp,
			NewNode: nil, // deletion
		})
	}

	if len(fixes) == 0 {
		return nil
	}

	return &ASTFixes{
		File:  f,
		Fset:  fset,
		Fixes: fixes,
	}
}
