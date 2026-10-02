package vet

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/analysis"
)

// hoistFixtureFile names the fixture so runAssertLint, which only rewrites
// test files, picks it up.
const hoistFixtureFile = "hoist_test.go"

// hoistFixture holds every init shape whose flattening must stay compilable.
// fakeT stands in for *testing.T: the rewriter keys on the receiver name and
// method, and the require stub's TestingT needs Errorf and FailNow.
const hoistFixture = `package main

import "strings"

// Pruning an import the rewrite leaves unused is the import fixer's job, not
// the rewriter's, so the import keeps one use of its own.
var _ = strings.Contains

type fakeT struct{}

func (fakeT) Errorf(format string, args ...interface{}) {}
func (fakeT) FailNow()                                   {}
func (fakeT) Fatal(args ...interface{})                  {}

func failure() string { return "" }

func count() int { return 0 }

func pair() (int, error) { return 0, nil }

// Sibling inits of the same name: the first declares, the second assigns.
func TestSiblings(t fakeT) {
	if msg := failure(); !strings.Contains(msg, "a") {
		t.Fatal(msg)
	}
	if msg := failure(); !strings.Contains(msg, "b") {
		t.Fatal(msg)
	}
}

// The name is declared again below the if, so the init stays scoped.
func TestLaterDeclaration(t fakeT) {
	if n := count(); n != 1 {
		t.Fatal("n")
	}
	n := 2
	_ = n
}

// The existing name has another type, so the init stays scoped.
func TestOtherType(t fakeT) {
	s := 0
	if s := failure(); !strings.Contains(s, "c") {
		t.Fatal(s)
	}
	_ = s
}

// One new name beside an existing one of the identical type keeps :=.
func TestMixed(t fakeT) {
	var err error
	if n, err := pair(); err != nil || n != 1 {
		t.Fatal("pair")
	}
	_ = err
}
`

// hoistImporter resolves the fixture's imports from source so the check needs
// no compiled standard library: strings as a one-function stand-in, and the
// testify require package as the hermetic stub under testdata.
type hoistImporter struct {
	t    *testing.T
	fset *token.FileSet
	pkgs map[string]*types.Package
}

func (im *hoistImporter) Import(path string) (*types.Package, error) {
	if pkg, ok := im.pkgs[path]; ok {
		return pkg, nil
	}
	var src string
	switch path {
	case "strings":
		src = "package strings\n\nfunc Contains(s, substr string) bool { return false }\n"
	case "github.com/stretchr/testify/require":
		stub, err := os.ReadFile(filepath.Join("testdata", "src", "testifystub", "require", "require.go"))
		require.NoError(im.t, err)
		src = string(stub)
	default:
		im.t.Fatalf("the fixture imports %q, which the test does not provide", path)
	}
	f, err := parser.ParseFile(im.fset, path+".go", src, 0)
	require.NoError(im.t, err)
	pkg, err := (&types.Config{Importer: im}).Check(path, im.fset, []*ast.File{f}, nil)
	require.NoError(im.t, err)
	im.pkgs[path] = pkg
	return pkg, nil
}

// checkHoistSource parses and type-checks one file through hoistImporter and
// returns the file with the type information the analyzer reads.
func checkHoistSource(t *testing.T, fset *token.FileSet, src string) (*ast.File, *types.Package, *types.Info, error) {
	t.Helper()
	f, err := parser.ParseFile(fset, hoistFixtureFile, src, parser.ParseComments)
	require.NoError(t, err)
	info := &types.Info{
		Types:  make(map[ast.Expr]types.TypeAndValue),
		Defs:   make(map[*ast.Ident]types.Object),
		Uses:   make(map[*ast.Ident]types.Object),
		Scopes: make(map[ast.Node]*types.Scope),
	}
	im := &hoistImporter{t: t, fset: fset, pkgs: make(map[string]*types.Package)}
	pkg, err := (&types.Config{Importer: im}).Check("main", fset, []*ast.File{f}, info)
	return f, pkg, info, err
}

// TestAssertLintHoistCompiles runs assertlint over hoistFixture, reprints the
// fixed file, and type-checks the result: the rewrite of an if's init must
// never leave a redeclaration or a mismatched assignment behind.
func TestAssertLintHoistCompiles(t *testing.T) {
	t.Serial()
	fset := token.NewFileSet()
	f, pkg, info, err := checkHoistSource(t, fset, hoistFixture)
	require.NoError(t, err, "the fixture itself must type-check")

	pass := &analysis.Pass{
		Analyzer:  AssertLintAnalyzer,
		Fset:      fset,
		Files:     []*ast.File{f},
		Pkg:       pkg,
		TypesInfo: info,
		Report:    func(analysis.Diagnostic) {},
	}
	result, err := runAssertLint(pass)
	require.NoError(t, err)
	fixes := result.([]*ASTFixes)
	require.Len(t, fixes, 1)
	require.Len(t, fixes[0].Fixes, 5, "every if in the fixture is rewritten")

	var buf strings.Builder
	require.NoError(t, fixes[0].Fprint(&buf))
	got := buf.String()

	_, _, _, err = checkHoistSource(t, token.NewFileSet(), got)
	require.NoError(t, err, "the rewritten file does not compile:\n%s", got)

	assert.Equal(t, 5, strings.Count(got, "require."), "one assertion per if:\n%s", got)
	assert.Equal(t, 2, strings.Count(got, "require.Contains(t, msg,"), got)
	assert.Contains(t, got, "msg := failure()", got)
	assert.Contains(t, got, "msg = failure()", got)
	assert.Contains(t, got, "n, err := pair()", "a new name beside an existing one keeps :=\n"+got)
	assert.Equal(t, 2, strings.Count(got, "\t{\n"), "the later-declared and other-typed names stay in a block:\n"+got)
}
