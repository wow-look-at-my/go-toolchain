package vet

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
)

// TestBannedOutputAnalyzer checks the fixture: direct fmt/log stdio writes
// report, Sprintf-style calls and non-stdio Fprint* writers must not.
func TestBannedOutputAnalyzer(t *testing.T) {
	t.Parallel() // analysistest loads real packages; each analyzer's dedup state is its own.
	testdata, err := filepath.Abs("testdata")
	require.Nil(t, err)
	analysistest.Run(t, testdata, BannedOutputAnalyzer, "bannedoutput")
}

// TestBannedOutputModuleScoping verifies the ban only fires inside the
// go-toolchain module. vetSemantic runs the analyzer on every consumer
// project go-toolchain builds; a consumer's fmt.Println must not be flagged
// (it has no src/logger to route through). An empty module path (drivers
// without module info, e.g. analysistest GOPATH fixtures) keeps the ban
// active.
func TestBannedOutputModuleScoping(t *testing.T) {
	t.Parallel() // Builds its own analysis.Pass in memory; no shared warned-map, no process-wide state.
	const src = `package main

import "fmt"

func main() { fmt.Println("hi") }
`
	cases := []struct {
		name        string
		module      *analysis.Module
		wantReports int
	}{
		{"go-toolchain module is checked", &analysis.Module{Path: bannedOutputModule}, 1},
		{"consumer module is skipped", &analysis.Module{Path: "example.com/consumer"}, 0},
		{"empty module path is checked", &analysis.Module{}, 1},
		{"nil module is checked", nil, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "/consumer/main.go", src, 0)
			require.NoError(t, err)
			var reports []analysis.Diagnostic
			pass := &analysis.Pass{
				Analyzer: BannedOutputAnalyzer,
				Fset:     fset,
				Files:    []*ast.File{f},
				Report:   func(d analysis.Diagnostic) { reports = append(reports, d) },
				Module:   c.module,
			}
			_, err = runBannedOutput(pass)
			require.NoError(t, err)
			require.Len(t, reports, c.wantReports)
		})
	}
}
