package vet

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
)

// TestSliceSetAnalyzer runs the fixture: every shape the check reports, and
// every neighbouring shape it must leave alone.
func TestSliceSetAnalyzer(t *testing.T) {
	t.Serial()
	testdata, err := filepath.Abs("testdata")
	require.NoError(t, err)
	analysistest.Run(t, testdata, SliceSetAnalyzer, "sliceset")
}

// TestSliceSetSeverityFollowsTheModule verifies the slice findings carry the
// same severity split as the map checks: an org module has the remedy a
// single org require away, so it fails; anywhere else the fix would add a
// dependency the author never chose, so it warns.
func TestSliceSetSeverityFollowsTheModule(t *testing.T) {
	t.Serial()
	const src = `package main

import "slices"

var known = []string{"a", "b"}

func main() { _ = slices.Contains(known, "a") }
`
	cases := []struct {
		name        string
		module      *analysis.Module
		wantReports int
		wantWarns   int64
	}{
		{"org module fails", &analysis.Module{Path: "github.com/wow-look-at-my/go-toolchain"}, 1, 0},
		{"PazerOP module fails", &analysis.Module{Path: "github.com/PazerOP/tool"}, 1, 0},
		{"third-party module warns", &analysis.Module{Path: "example.com/consumer"}, 0, 1},
		{"nil module fails", nil, 1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resetSliceSetWarnings()
			before := logger.WarnCount()
			reports := runSliceSetOnSource(t, src, c.module)
			require.Len(t, reports, c.wantReports)
			require.Equal(t, before+c.wantWarns, logger.WarnCount())
		})
	}
}

// TestSliceSetLeavesUncertainSlicesAlone pins the shapes that must stay
// silent. Each shape reads position or repetition, or puts the slice somewhere
// this walk cannot follow, so a set is not provably what the author meant.
func TestSliceSetLeavesUncertainSlicesAlone(t *testing.T) {
	t.Serial()
	for _, c := range []struct {
		name string
		body string
	}{
		{"sorted", "s := []string{\"b\", \"a\"}\nslices.Sort(s)\n_ = slices.Contains(s, \"a\")"},
		{"sliced", "s := []string{\"a\"}\n_ = s[1:]\n_ = slices.Contains(s, \"a\")"},
		{"spread into another slice", "s := []string{\"a\"}\nvar o []string\no = append(o, s...)\n_ = slices.Contains(s, \"a\")\n_ = o"},
		{"ranged by index", "s := []string{\"a\"}\nfor i := range s {\n_ = i\n}\n_ = slices.Contains(s, \"a\")"},
		{"made with a length", "s := make([]string, 3)\n_ = slices.Contains(s, \"a\")"},
		{"never asked", "s := []string{\"a\"}\ns = append(s, \"b\")\n_ = len(s)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := "package main\n\nimport \"slices\"\n\nvar _ = slices.Contains\n\nfunc main() {\n" + c.body + "\n}\n"
			require.Empty(t, runSliceSetOnSource(t, src, nil))
		})
	}
}

// runSliceSetOnSource type-checks src and returns what the analyzer reports.
// The import of slices stays unresolved: supplying it means building its
// export data up front, and the analyzer reads a slices call off the selector
// when the type checker could not resolve it.
func runSliceSetOnSource(t *testing.T, src string, module *analysis.Module) []analysis.Diagnostic {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "/consumer/main.go", src, parser.ParseComments)
	require.NoError(t, err)

	info := &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{},
		Defs:  map[*ast.Ident]types.Object{},
		Uses:  map[*ast.Ident]types.Object{},
	}
	conf := types.Config{Error: func(error) {}}
	pkg, _ := conf.Check("main", fset, []*ast.File{file}, info)

	var reports []analysis.Diagnostic
	pass := &analysis.Pass{
		Analyzer:  SliceSetAnalyzer,
		Fset:      fset,
		Files:     []*ast.File{file},
		Pkg:       pkg,
		Report:    func(d analysis.Diagnostic) { reports = append(reports, d) },
		Module:    module,
		TypesInfo: info,
	}
	_, err = runSliceSet(pass)
	require.NoError(t, err)
	return reports
}
