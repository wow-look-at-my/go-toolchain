package vet

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
)

// TestMapSetAnalyzer runs the mapset analyzer over its fixture: a struct{}
// value map and an all-true bool map literal must be reported, while a lookup
// table, a non-bool map, an empty literal and a marked declaration must not.
func TestMapSetAnalyzer(t *testing.T) {
	testdata, err := filepath.Abs("testdata")
	require.NoError(t, err)
	analysistest.Run(t, testdata, MapSetAnalyzer, "mapset")
}

// TestMapSetModuleScoping verifies the check only fires on org code.
// go-toolchain vets every project it builds, and the remedy adds an org
// dependency, so a third-party consumer must not get a red build over it.
func TestMapSetModuleScoping(t *testing.T) {
	const src = `package main

var seen = map[string]struct{}{}

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
