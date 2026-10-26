// releaseVarLDFlags fills a program's own version variable with the release stamp.

package cmd

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/gomod"
)

// releaseVarNames are the variables a release build fills with "<release> (<commit>)".
var releaseVarNames = []string{"Version", "version"}

// releaseVarLDFlags is the -X list that fills the version variables of the
// main package and of each package of this module that it imports. It is
// empty for a build with no release. The -X reaches only the link, so no
// compile result in the build cache changes. Depth: docs/RELEASE-STAMP.md.
func releaseVarLDFlags(importPath string) string {
	value := releaseVarValue(os.Getenv(gosmoReleaseEnv), os.Getenv(gosmoReleaseRevisionEnv))
	if value == "" {
		return ""
	}
	mod := gomod.ReadModulePath(".")
	dir := packageDir(mod, importPath)
	if dir == "" {
		return ""
	}
	var flags []string
	if f := releaseVarStamp(importPath, gomod.PackageStringVars(dir), value); f != "" {
		flags = append(flags, f)
	}
	for _, pkg := range moduleImports(mod, dir) {
		if f := releaseVarStamp(pkg, gomod.PackageStringVars(packageDir(mod, pkg)), value); f != "" {
			flags = append(flags, f)
		}
	}
	return strings.Join(flags, " ")
}

// releaseVarValue is "<release> (<commit>)", or "" when the build has no release.
func releaseVarValue(release, revision string) string {
	if release == "" || revision == "" {
		return ""
	}
	return release + " (" + revision + ")"
}

// releaseVarStamp writes the -X flags for the version variables that declared
// holds. The value holds a space, so each flag field is quoted as a whole. The
// go command splits -ldflags the way cmd/internal/quoted.Split does.
func releaseVarStamp(importPath string, declared set.Set[string], value string) string {
	var flags []string
	for _, name := range releaseVarNames {
		if declared.Contains(name) {
			flags = append(flags, "-X '"+importPath+"."+name+"="+value+"'")
		}
	}
	return strings.Join(flags, " ")
}

// moduleImports lists the packages of module mod that the package in dir
// imports, from its non-test files.
func moduleImports(mod, dir string) []string {
	if mod == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	seen := set.New[string]()
	var pkgs []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, _ := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, parser.ImportsOnly)
		if f == nil {
			continue
		}
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil || !strings.HasPrefix(path, mod+"/") || seen.Contains(path) {
				continue
			}
			seen.Add(path)
			pkgs = append(pkgs, path)
		}
	}
	return pkgs
}
