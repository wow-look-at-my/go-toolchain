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

// TestMapSetAnalyzer runs the mapset analyzer over its fixture: an all-true
// bool map literal and a made-empty bool map used only as a set must be
// reported, while a lookup table, a comma-ok read, a computed value, a map
// that escapes, a map[K]struct{} and a marked declaration must not.
func TestMapSetAnalyzer(t *testing.T) {
	testdata, err := filepath.Abs("testdata")
	require.NoError(t, err)
	analysistest.Run(t, testdata, MapSetAnalyzer, "mapset")
}

// TestMapSetSkipsTheSetPackageItself verifies the remedy never warns about
// itself: Set[T] IS a map[T]struct{}, and its storage sites would spend half
// the warnings budget saying so.
func TestMapSetSkipsTheSetPackageItself(t *testing.T) {
	const src = `package set

type Set[T comparable] struct {
	m map[T]struct{}
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "/set/set.go", src, parser.ParseComments)
	require.NoError(t, err)

	for _, c := range []struct {
		path      string
		wantWarns int64
	}{
		{setPackage, 0},
		{setPackage + "_test", 0},
		{"github.com/wow-look-at-my/other", 1},
	} {
		t.Run(c.path, func(t *testing.T) {
			resetMapSetWarnings()
			before := logger.WarnCount()
			pass := &analysis.Pass{
				Analyzer:  MapSetAnalyzer,
				Fset:      fset,
				Files:     []*ast.File{f},
				Pkg:       types.NewPackage(c.path, "set"),
				Report:    func(analysis.Diagnostic) { t.Fatal("a struct map must never fail the build") },
				TypesInfo: &types.Info{},
			}
			_, err := runMapSet(pass)
			require.NoError(t, err)
			require.Equal(t, before+c.wantWarns, logger.WarnCount())
		})
	}
}

// TestMapSetModuleScoping verifies the check only fires on org code.
// go-toolchain vets every project it builds, and the remedy adds an org
// dependency, so a third-party consumer must not get a red build over it.
func TestMapSetModuleScoping(t *testing.T) {
	const src = `package main

var seen = map[string]bool{"a": true}

func main() { _ = seen }
`
	cases := []struct {
		name        string
		module      *analysis.Module
		wantReports int
	}{
		{"org module is checked", &analysis.Module{Path: "github.com/wow-look-at-my/go-toolchain"}, 1},
		{"PazerOP module is checked", &analysis.Module{Path: "github.com/PazerOP/tool"}, 1},
		{"third-party module is skipped", &analysis.Module{Path: "example.com/consumer"}, 0},
		{"empty module path is checked", &analysis.Module{}, 1},
		{"nil module is checked", nil, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "/consumer/main.go", src, parser.ParseComments)
			require.NoError(t, err)
			var reports []analysis.Diagnostic
			pass := &analysis.Pass{
				Analyzer:  MapSetAnalyzer,
				Fset:      fset,
				Files:     []*ast.File{f},
				Report:    func(d analysis.Diagnostic) { reports = append(reports, d) },
				Module:    c.module,
				TypesInfo: &types.Info{},
			}
			_, err = runMapSet(pass)
			require.NoError(t, err)
			require.Len(t, reports, c.wantReports)
		})
	}
}

// TestMapSetStructMapOnlyWarns verifies a map[K]struct{} never fails a build.
// It already carries no value; the diagnostic is reserved for the map[K]bool
// default, and the struct map gets a warning naming set.Set instead.
func TestMapSetStructMapOnlyWarns(t *testing.T) {
	const src = `package main

var seen = map[string]struct{}{}

func main() { seen["a"] = struct{}{} }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "/consumer/main.go", src, parser.ParseComments)
	require.NoError(t, err)

	resetMapSetWarnings()
	before := logger.WarnCount()
	var reports []analysis.Diagnostic
	pass := &analysis.Pass{
		Analyzer:  MapSetAnalyzer,
		Fset:      fset,
		Files:     []*ast.File{f},
		Report:    func(d analysis.Diagnostic) { reports = append(reports, d) },
		TypesInfo: &types.Info{},
	}
	_, err = runMapSet(pass)
	require.NoError(t, err)
	require.Empty(t, reports)
	require.Equal(t, before+1, logger.WarnCount())

	// go/packages walks the same file once per package variant. One site
	// spends one warning of the budget, until the next run resets the record.
	_, err = runMapSet(pass)
	require.NoError(t, err)
	require.Equal(t, before+1, logger.WarnCount())

	resetMapSetWarnings()
	_, err = runMapSet(pass)
	require.NoError(t, err)
	require.Equal(t, before+2, logger.WarnCount())
}
