package test

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/gomod"
)

// HasCoverableStatements reports whether any non-test, non-generated Go file
// under dir that is part of the current build contains at least one function
// body with a statement — i.e. whether `go test -cover` could ever measure a
// statement in this module. Embed-only and declarations-only modules (no
// function bodies anywhere) return false: their empty coverage profile is
// expected, not evidence of a broken setup.
//
// The walk mirrors listTestPackages: hidden directories, vendor/, and
// testdata/ are skipped, as are nested modules (their files belong to a
// different module and are invisible to this module's "./..."). Generated
// files are skipped because filterBlocksByGenerated excludes them from
// coverage totals, and files excluded by build constraints (e.g. a
// "//go:build ignore" generator) are skipped because `go test` never
// compiles or instruments them.
func HasCoverableStatements(dir string) bool {
	found := false
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			name := d.Name()
			if path != dir && (strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" || gomod.IsNestedModule(path)) {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		if isGeneratedFile(path) {
			return nil
		}
		// Honor build constraints so a tag-excluded file (never compiled,
		// never instrumented) cannot count as coverable. An error means
		// "can't classify" — treat the file as included, matching
		// gomod.fileMatchesBuild.
		if matched, matchErr := build.Default.MatchFile(filepath.Dir(path), name); matchErr == nil && !matched {
			return nil
		}
		if fileHasFuncBody(path) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// fileHasFuncBody reports whether the Go file contains any function
// declaration or function literal with a non-empty body. Package-level var,
// const, and type declarations carry no coverable statements, and neither
// does an empty function body (`go test -cover` reports such packages as
// "[no statements]").
func fileHasFuncBody(path string) bool {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil || f == nil {
		return false
	}
	has := false
	ast.Inspect(f, func(n ast.Node) bool {
		if has {
			return false
		}
		switch fn := n.(type) {
		case *ast.FuncDecl:
			if fn.Body != nil && len(fn.Body.List) > 0 {
				has = true
			}
		case *ast.FuncLit:
			if fn.Body != nil && len(fn.Body.List) > 0 {
				has = true
			}
		}
		return !has
	})
	return has
}
