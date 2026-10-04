package vet

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/gomod"
)

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

// FixUnusedRangeVars scans all Go files and blanks unused range loop variables.
func FixUnusedRangeVars(pattern string) ([]string, error) {
	var files []string
	if pattern == "./..." {
		err := filepath.WalkDir(".", func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == "vendor" || d.Name() == ".git") {
				return filepath.SkipDir
			}
			// Never rewrite a nested module's files.
			if d.IsDir() && gomod.IsNestedModule(p) {
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
		wasFixed, err := fixFileUnusedRangeVars(file)
		if err != nil {
			return fixed, fmt.Errorf("fixing %s: %w", file, err)
		}
		if wasFixed {
			fixed = append(fixed, file)
		}
	}

	return fixed, nil
}

func fixFileUnusedRangeVars(filename string) (bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		return false, err
	}

	// Collect all range statement variables
	type rangeVar struct {
		ident *ast.Ident
		scope *ast.RangeStmt
	}
	var rangeVars []rangeVar

	ast.Inspect(f, func(n ast.Node) bool {
		rs, ok := n.(*ast.RangeStmt)
		if !ok {
			return true
		}
		if rs.Key != nil {
			if ident, ok := rs.Key.(*ast.Ident); ok && ident.Name != "_" {
				rangeVars = append(rangeVars, rangeVar{ident, rs})
			}
		}
		if rs.Value != nil {
			if ident, ok := rs.Value.(*ast.Ident); ok && ident.Name != "_" {
				rangeVars = append(rangeVars, rangeVar{ident, rs})
			}
		}
		return true
	})

	if len(rangeVars) == 0 {
		return false, nil
	}

	// Check which range vars are used in their scope
	modified := false
	for _, rv := range rangeVars {
		used := false
		InspectNode(rv.scope.Body, func(n ast.Node) bool {
			if ident, ok := n.(*ast.Ident); ok && ident != rv.ident && ident.Name == rv.ident.Name {
				used = true
				return false
			}
			return true
		})
		if !used {
			rv.ident.Name = "_"
			modified = true
		}
	}

	if !modified {
		return false, nil
	}

	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, f); err != nil {
		return false, err
	}
	// go/printer tab-aligns and rewrites doc-comment quotes; canonicalize to gofmt style before comparing.
	if err := os.WriteFile(filename, canonicalizeGoSource(buf.Bytes()), 0o644); err != nil {
		return false, err
	}

	return true, nil
}
