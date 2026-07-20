package summary

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	gotest "github.com/wow-look-at-my/go-toolchain/src/test"
)

func buildTestLocationCache(cases []gotest.TestCaseResult, modulePath string) map[string]testFuncLocation {
	cache := make(map[string]testFuncLocation)

	// Collect unique package+func pairs to look up
	type lookupKey struct {
		pkg      string
		funcName string
	}
	needed := make(map[lookupKey]bool)
	for _, tc := range cases {
		needed[lookupKey{tc.Package, rootTestFunc(tc.Test)}] = true
	}

	// Group by package to avoid re-walking
	pkgFuncs := make(map[string]map[string]bool)
	for k := range needed {
		if pkgFuncs[k.pkg] == nil {
			pkgFuncs[k.pkg] = make(map[string]bool)
		}
		pkgFuncs[k.pkg][k.funcName] = true
	}

	for pkg, funcs := range pkgFuncs {
		dir := pkgToDir(pkg, modulePath)
		if dir == "" {
			continue
		}
		locs := findTestFuncsInDir(dir, funcs)
		for funcName, loc := range locs {
			cacheKey := pkg + "." + funcName
			cache[cacheKey] = loc
		}
	}

	return cache
}

// pkgToDir converts a Go package import path to a repo-relative directory.
func pkgToDir(pkg, modulePath string) string {
	if modulePath != "" && strings.HasPrefix(pkg, modulePath) {
		rel := strings.TrimPrefix(pkg, modulePath)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			return "."
		}
		return rel
	}
	// Fallback: try stripping common github.com/owner/repo prefix
	parts := strings.SplitN(pkg, "/", 4)
	if len(parts) >= 4 {
		return parts[3]
	}
	if len(parts) == 3 {
		return "."
	}
	return ""
}

// findTestFuncsInDir parses _test.go files in a directory and returns locations
// for the requested function names.
func findTestFuncsInDir(dir string, funcNames map[string]bool) map[string]testFuncLocation {
	result := make(map[string]testFuncLocation)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return result
	}

	fset := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			continue
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			if funcNames[fn.Name.Name] {
				pos := fset.Position(fn.Pos())
				result[fn.Name.Name] = testFuncLocation{
					file: path,
					line: pos.Line,
				}
			}
		}
	}

	return result
}

func sourceURL(tc gotest.TestCaseResult, commitSHA, repo, modulePath string, cache map[string]testFuncLocation) string {
	if commitSHA == "" || repo == "" {
		return ""
	}

	funcName := rootTestFunc(tc.Test)
	cacheKey := tc.Package + "." + funcName
	loc, ok := cache[cacheKey]
	if !ok {
		return ""
	}

	return fmt.Sprintf("https://github.com/%s/blob/%s/%s#L%d", repo, commitSHA, loc.file, loc.line)
}
