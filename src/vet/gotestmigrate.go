package vet

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	ansi "github.com/wow-look-at-my/ansi-writer"
	"github.com/wow-look-at-my/go-toolchain/src/gomod"
)

const (
	gotestAssert    = "gotest.tools/v3/assert"
	gotestAssertCmp = "gotest.tools/v3/assert/cmp"
	testifyRequire  = "github.com/stretchr/testify/require"
	testifyAssert   = "github.com/stretchr/testify/assert"
)

// gotestFuncRenames maps gotest.tools function names to their testify/require equivalents.
var gotestFuncRenames = map[string]string{
	"Assert":    "True",
	"NilError":  "NoError",
	"Error":     "EqualError",
	"DeepEqual": "Equal",
	// These map to the same name — listed for clarity
	"ErrorContains": "ErrorContains",
	"ErrorIs":       "ErrorIs",
	"Equal":         "Equal",
}

// cmpToTestify maps gotest.tools/v3/assert/cmp function names to testify equivalents.
var cmpToTestify = map[string]string{
	"Equal":         "Equal",
	"DeepEqual":     "Equal",
	"Nil":           "Nil",
	"ErrorContains": "ErrorContains",
	"Error":         "EqualError",
	"ErrorIs":       "ErrorIs",
	"Len":           "Len",
	"Contains":      "Contains",
	"Panics":        "Panics",
	"Regexp":        "Regexp",
}

// extractCmpCall checks if an expression is a cmp.X() call and returns the
// call expression and selector if so.
func extractCmpCall(expr ast.Expr) (*ast.CallExpr, *ast.SelectorExpr, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return nil, nil, false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, nil, false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil, nil, false
	}
	if ident.Name != "cmp" {
		return nil, nil, false
	}
	return call, sel, true
}

// MigrateGotestTools scans all Go files for gotest.tools/v3/assert imports and
// routes each offending file through ed: a fix-mode editor migrates it to
// github.com/stretchr/testify and resyncs the module graph (go mod tidy, plus
// go mod vendor when vendored); a check-mode (CI) editor records a violation
// instead, so a tree still on gotest.tools fails CI rather than passing green —
// the same enforcement FixTestifyImports gives the removed testify fork.
//
// Returns whether any file was written (only possible with a fix-mode editor).
func MigrateGotestTools(ed Editor) (bool, error) {
	var anyWrote bool

	err := filepath.WalkDir(".", func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "vendor" || d.Name() == ".git" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		// Never rewrite a nested module's files (e.g. src/compat/go-isatty).
		if d.IsDir() && gomod.IsNestedModule(p) {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}
		wrote, err := migrateFileGotestTools(ed, p)
		if err != nil {
			return err
		}
		if wrote {
			anyWrote = true
		}
		return nil
	})

	if err != nil {
		return false, err
	}

	if anyWrote {
		if err := syncModuleGraph(); err != nil {
			return anyWrote, err
		}
	}

	return anyWrote, nil
}

// migrateFileGotestTools rewrites gotest.tools imports/calls in a single file
// and routes the result through ed (write locally, record a violation on CI).
// Returns whether the file was written.
func migrateFileGotestTools(ed Editor, filename string) (bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		// Unparseable file: skip; the type-check/go vet pass reports the syntax
		// error with a proper location.
		return false, nil
	}

	var hasAssertImport bool  // tracks if gotest.tools/v3/assert was present
	var hasCmpImport bool     // tracks if gotest.tools/v3/assert/cmp was present
	var needAssertImport bool // whether to add testify/assert import

	// Check if testify/require is already imported (before we rewrite gotest.tools)
	hasExistingRequire := false
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) == testifyRequire {
			hasExistingRequire = true
			break
		}
	}

	// Phase 1: Rewrite imports
	var gotestImportSpec *ast.ImportSpec
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)

		switch path {
		case gotestAssert:
			hasAssertImport = true
			gotestImportSpec = imp

		case gotestAssertCmp:
			hasCmpImport = true
		}
	}

	if !hasAssertImport {
		return false, nil
	}

	if hasExistingRequire {
		// File already has testify/require — just remove the gotest.tools import
		removeImport(f, gotestImportSpec)
	} else {
		// Rewrite gotest.tools/v3/assert → testify/require in-place
		gotestImportSpec.Path.Value = `"` + testifyRequire + `"`
		if gotestImportSpec.Name != nil && gotestImportSpec.Name.Name == "assert" {
			gotestImportSpec.Name.Name = "require"
		}
	}

	// Record fixes to print only if the change is actually written (fix mode).
	fixLog := [][2]string{{gotestAssert, testifyRequire}}

	// Phase 2: Walk call expressions to rename functions and unwrap cmp calls.
	// Track which idents should stay as "assert" (non-fatal Check paths).
	keepAsAssert := map[*ast.Ident]bool{}

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

		// Handle Check/Assert with cmp.X() argument — unwrap into direct testify call
		if (funcName == "Check" || funcName == "Assert") && len(call.Args) >= 2 {
			if cmpCall, cmpSel, ok := extractCmpCall(call.Args[1]); ok {
				if testifyName, ok := cmpToTestify[cmpSel.Sel.Name]; ok {
					sel.Sel.Name = testifyName
					// Replace args: [t, cmp.X(a, b)] → [t, a, b]
					cmpArgs := cmpCall.Args
					if cmpSel.Sel.Name == "DeepEqual" && len(cmpArgs) > 2 {
						cmpArgs = cmpArgs[:2] // drop go-cmp options
					}
					call.Args = append(call.Args[:1], cmpArgs...)

					if funcName == "Check" {
						keepAsAssert[ident] = true
						needAssertImport = true
					} else {
						ident.Name = "require"
					}

					return true
				}
			}
		}

		// Check without cmp → assert.True (non-fatal)
		if funcName == "Check" {
			needAssertImport = true
			keepAsAssert[ident] = true
			sel.Sel.Name = "True"

			return true
		}

		// Everything else: rename to require
		ident.Name = "require"
		if newName, ok := gotestFuncRenames[funcName]; ok {
			sel.Sel.Name = newName
		}

		return true
	})

	// Phase 2b: Rename any remaining assert.X selectors in non-call contexts
	// (type references, value accesses, etc.)
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if ident.Name == "assert" && !keepAsAssert[ident] {
			ident.Name = "require"

		}
		return true
	})

	// Phase 3: Remove cmp import if present
	if hasCmpImport {
		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, `"`) == gotestAssertCmp {
				removeImport(f, imp)
				break
			}
		}
		fixLog = append(fixLog, [2]string{gotestAssertCmp, "(removed)"})
	}

	// Phase 4: Add testify/assert import if non-fatal (Check) calls were found
	if needAssertImport {
		addImport(f, testifyAssert)
	}

	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, f); err != nil {
		return false, err
	}
	// go/printer tab-aligns, leaves the new imports unsorted, and rewrites
	// doc-comment quotes; canonicalize to gofmt style and restore literal quotes
	// so the rewritten file is what RunGofmt expects.
	out := canonicalizeGoSource(buf.Bytes())
	wrote, err := ed.Require(filename, out, "imports gotest.tools/v3/assert; migrate to github.com/stretchr/testify")
	if err != nil {
		return false, err
	}
	if wrote {
		for _, fx := range fixLog {
			printGotestFix(filename, fx[0], fx[1])
		}
	}
	return wrote, nil
}

// addImport adds an import path to the file's first import declaration.
// Does nothing if the import already exists.
func addImport(f *ast.File, path string) {
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) == path {
			return // already imported
		}
	}

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
