package vet

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/packages"
)

// Run executes all custom analyzers on the current module.
// Returns error if any diagnostics are found.
// Returns nil if no packages are found (e.g., in test environments).
func Run() error {
	err := RunOnPattern("./...")
	// Gracefully handle "no main module" errors from test environments
	if err != nil && strings.Contains(err.Error(), "does not contain main module") {
		return nil
	}
	return err
}

// Fix runs the analyzer with -fix to auto-fix violations.
// It uses the current go-toolchain binary as the vettool.
func Fix() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to find executable: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("failed to resolve executable path: %w", err)
	}

	cmd := exec.Command("go", "vet", "-vettool="+exe, "-fix", "./...")
	cmd.Env = append(os.Environ(), "GO_TOOLCHAIN_VETTOOL=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// go vet returns non-zero if there are findings, but fixes are still applied
	_ = cmd.Run()
	return nil
}

// RunOnPattern executes all custom analyzers on packages matching the pattern.
func RunOnPattern(pattern string) error {
	cfg := &packages.Config{
		Mode: packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes |
			packages.NeedTypesInfo | packages.NeedName | packages.NeedImports,
		Tests: true,
	}

	pkgs, err := packages.Load(cfg, pattern)
	if err != nil {
		return fmt.Errorf("failed to load packages: %w", err)
	}

	// Check for load errors
	var loadErrors []string
	for _, pkg := range pkgs {
		for _, e := range pkg.Errors {
			loadErrors = append(loadErrors, e.Error())
		}
	}
	if len(loadErrors) > 0 {
		return fmt.Errorf("package load errors:\n%s", strings.Join(loadErrors, "\n"))
	}

	// Run analyzers
	analyzers := []*analysis.Analyzer{
		AssertLintAnalyzer,
	}

	var diagnostics []Diagnostic
	for _, analyzer := range analyzers {
		diags, err := runAnalyzer(analyzer, pkgs)
		if err != nil {
			return fmt.Errorf("analyzer %s failed: %w", analyzer.Name, err)
		}
		diagnostics = append(diagnostics, diags...)
	}

	if len(diagnostics) == 0 {
		return nil
	}

	// Sort diagnostics by file, then line
	sort.Slice(diagnostics, func(i, j int) bool {
		if diagnostics[i].File != diagnostics[j].File {
			return diagnostics[i].File < diagnostics[j].File
		}
		return diagnostics[i].Line < diagnostics[j].Line
	})

	// Format error message
	var sb strings.Builder
	sb.WriteString("custom vet found issues:\n")
	for _, d := range diagnostics {
		fmt.Fprintf(&sb, "%s:%d:%d: %s\n", d.File, d.Line, d.Column, d.Message)
	}
	return fmt.Errorf("%s", sb.String())
}

// Diagnostic represents a single analyzer finding.
type Diagnostic struct {
	File    string
	Line    int
	Column  int
	Message string
}

// runAnalyzer runs a single analyzer on the given packages.
func runAnalyzer(a *analysis.Analyzer, pkgs []*packages.Package) ([]Diagnostic, error) {
	var diagnostics []Diagnostic

	for _, pkg := range pkgs {
		pass := &analysis.Pass{
			Analyzer:  a,
			Fset:      pkg.Fset,
			Files:     pkg.Syntax,
			Pkg:       pkg.Types,
			TypesInfo: pkg.TypesInfo,
			Report: func(d analysis.Diagnostic) {
				pos := pkg.Fset.Position(d.Pos)
				diagnostics = append(diagnostics, Diagnostic{
					File:    pos.Filename,
					Line:    pos.Line,
					Column:  pos.Column,
					Message: d.Message,
				})
			},
		}

		_, err := a.Run(pass)
		if err != nil {
			return nil, err
		}
	}

	return diagnostics, nil
}
