package gomod

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
)

// PackageStringVars returns the package-level string variables dir declares.
//
// The linker FAILS a -X naming a variable of another type, so a caller has to
// prove the type before it stamps a name. Only an explicit string type or a
// string-literal initializer counts.
func PackageStringVars(dir string) set.Set[string] {
	names := set.New[string]()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return names
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		addFileStringVars(filepath.Join(dir, name), &names)
	}
	return names
}

// addFileStringVars adds the file's package-level string variables to names.
// A file that does not parse contributes whatever the partial AST carries.
func addFileStringVars(path string, names *set.Set[string]) {
	f, _ := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if f == nil {
		return
	}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || !declaresStrings(vs) {
				continue
			}
			for _, id := range vs.Names {
				names.Add(id.Name)
			}
		}
	}
}

// declaresStrings reports whether the spec's variables are all strings.
func declaresStrings(vs *ast.ValueSpec) bool {
	if id, isIdent := vs.Type.(*ast.Ident); isIdent {
		return id.Name == "string"
	}
	// A named type resolves to a defined type, which -X refuses, so only an
	// untyped declaration reaches the literal check below.
	if vs.Type != nil || len(vs.Values) != len(vs.Names) {
		return false
	}
	for _, v := range vs.Values {
		lit, isLit := v.(*ast.BasicLit)
		if !isLit || lit.Kind != token.STRING {
			return false
		}
	}
	return len(vs.Values) > 0
}
