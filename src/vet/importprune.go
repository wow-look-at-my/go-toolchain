// importLocalNames maps each import to the name the file uses for it.

package vet

import (
	"go/ast"
	"go/token"
	"go/types"
	"strconv"

	"github.com/wow-look-at-my/go-containers/set"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/astutil"
)

// importLocalNames maps each named or plain import of file to the name the
// file qualifies it with. The type checker supplies the name, because a
// package name need not match its path. Blank and dot imports are absent:
// no selector names them.
func importLocalNames(pass *analysis.Pass, file *ast.File) map[*ast.ImportSpec]string {
	names := make(map[*ast.ImportSpec]string)
	for _, spec := range file.Imports {
		var obj types.Object
		if spec.Name != nil {
			if spec.Name.Name == "_" || spec.Name.Name == "." {
				continue
			}
			obj = pass.TypesInfo.Defs[spec.Name]
		} else {
			obj = pass.TypesInfo.Implicits[spec]
		}
		if pkg, ok := obj.(*types.PkgName); ok {
			names[spec] = pkg.Name()
		}
	}
	return names
}

// pruneUnusedImports deletes each import in names that no selector of file
// qualifies any more. A fix that replaces strings.Contains with
// assert.Contains removes a use, and an import with no use does not compile.
// A selector on a local variable of the same name keeps the import, which
// can leave an unused import but never removes a used one.
func pruneUnusedImports(fset *token.FileSet, file *ast.File, names map[*ast.ImportSpec]string) {
	if len(names) == 0 {
		return
	}
	qualifiers := set.New[string]()
	for _, decl := range file.Decls {
		if gd, ok := decl.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
			continue
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok {
					qualifiers.Add(id.Name)
				}
			}
			return true
		})
	}
	for _, spec := range append([]*ast.ImportSpec(nil), file.Imports...) {
		name, ok := names[spec]
		if !ok || qualifiers.Contains(name) {
			continue
		}
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		alias := ""
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		astutil.DeleteNamedImport(fset, file, alias, path)
	}
}
