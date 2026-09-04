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

// TestMapSetAnalyzer: an all-true or emptied-then-set-only bool map is reported; a lookup
// table, comma-ok read, computed value, escaping map, or map[K]struct{} is not.
func TestMapSetAnalyzer(t *testing.T) {
	t.Serial()
	t.Parallel() // See TestBannedOutputAnalyzer.
	testdata, err := filepath.Abs("testdata")
	require.NoError(t, err)
	analysistest.Run(t, testdata, MapSetAnalyzer, "mapset")
}

// TestMapSetSkipsTheSetPackageItself verifies the remedy never warns about
// itself: Set[T] IS a map[T]struct{}, and its storage sites would spend half
// the warnings budget saying so.
func TestMapSetSkipsTheSetPackageItself(t *testing.T) {
	t.Serial()
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

// TestMapSetSeverityFollowsTheModule verifies no module escapes the check, and
// that what the module decides is severity. An org module has the remedy a
// single org require away, so its findings fail the build; anywhere else the
// same finding is a warning, because the fix would add a dependency the author
// never chose.
func TestMapSetSeverityFollowsTheModule(t *testing.T) {
	t.Serial()
	const src = `package main

var seen = map[string]bool{"a": true}

func main() { _ = seen }
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
		{"empty module path fails", &analysis.Module{}, 1, 0},
		{"nil module fails", nil, 1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
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
				Module:    c.module,
				TypesInfo: &types.Info{},
			}
			_, err = runMapSet(pass)
			require.NoError(t, err)
			require.Len(t, reports, c.wantReports)
			require.Equal(t, before+c.wantWarns, logger.WarnCount())
		})
	}
}

// TestMapSetStructMapOnlyWarns verifies a map[K]struct{} never fails a build,
// even in an org module. It already carries no value; the diagnostic is
// reserved for the map[K]bool default.
func TestMapSetStructMapOnlyWarns(t *testing.T) {
	t.Serial()
	const src = `package main

var seen = map[string]struct{}{}

func main() { seen["a"] = struct{}{} }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "/consumer/main.go", src, parser.ParseComments)
	require.NoError(t, err)

	resetMapSetWarnings()
	before := logger.TotalWarnCount()
	beforeDistinct := logger.WarnCount()
	pass := &analysis.Pass{
		Analyzer:  MapSetAnalyzer,
		Fset:      fset,
		Files:     []*ast.File{f},
		Module:    &analysis.Module{Path: "github.com/wow-look-at-my/go-toolchain"},
		Report:    func(analysis.Diagnostic) { t.Fatal("a struct map must never fail the build") },
		TypesInfo: &types.Info{},
	}
	_, err = runMapSet(pass)
	require.NoError(t, err)
	require.Equal(t, before+1, logger.TotalWarnCount())

	// Every package variant walks the same file; the dedup record keeps this site from printing again until reset.
	_, err = runMapSet(pass)
	require.NoError(t, err)
	require.Equal(t, before+1, logger.TotalWarnCount())

	resetMapSetWarnings()
	_, err = runMapSet(pass)
	require.NoError(t, err)
	require.Equal(t, before+2, logger.TotalWarnCount())

	// The site printed repeatedly, and it is a single site. The budget counts it as such.
	require.Equal(t, beforeDistinct+1, logger.WarnCount())
}
