package vet

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/analysis/passes/assign"
	"golang.org/x/tools/go/analysis/passes/atomic"
	"golang.org/x/tools/go/analysis/passes/bools"
	"golang.org/x/tools/go/analysis/passes/buildtag"
	"golang.org/x/tools/go/analysis/passes/composite"
	"golang.org/x/tools/go/analysis/passes/copylock"
	"golang.org/x/tools/go/analysis/passes/errorsas"
	"golang.org/x/tools/go/analysis/passes/httpresponse"
	"golang.org/x/tools/go/analysis/passes/loopclosure"
	"golang.org/x/tools/go/analysis/passes/lostcancel"
	"golang.org/x/tools/go/analysis/passes/nilfunc"
	"golang.org/x/tools/go/analysis/passes/printf"
	"golang.org/x/tools/go/analysis/passes/shift"
	"golang.org/x/tools/go/analysis/passes/stdmethods"
	"golang.org/x/tools/go/analysis/passes/stringintconv"
	"golang.org/x/tools/go/analysis/passes/structtag"
	"golang.org/x/tools/go/analysis/passes/tests"
	"golang.org/x/tools/go/analysis/passes/unmarshal"
	"golang.org/x/tools/go/analysis/passes/unreachable"
	"golang.org/x/tools/go/analysis/passes/unsafeptr"
	"golang.org/x/tools/go/analysis/passes/unusedresult"
	"golang.org/x/tools/go/packages"
)

// Analyzers returns all analyzers to run (standard + custom).
func Analyzers() []*analysis.Analyzer {
	return []*analysis.Analyzer{
		// Standard go vet analyzers
		assign.Analyzer,
		atomic.Analyzer,
		bools.Analyzer,
		buildtag.Analyzer,
		composite.Analyzer,
		copylock.Analyzer,
		errorsas.Analyzer,
		httpresponse.Analyzer,
		loopclosure.Analyzer,
		lostcancel.Analyzer,
		nilfunc.Analyzer,
		printf.Analyzer,
		shift.Analyzer,
		stdmethods.Analyzer,
		stringintconv.Analyzer,
		structtag.Analyzer,
		tests.Analyzer,
		unmarshal.Analyzer,
		unreachable.Analyzer,
		unsafeptr.Analyzer,
		unusedresult.Analyzer,
		// Custom analyzers
		AssertLintAnalyzer,
	}
}

// Run executes all analyzers on the current module.
// If fix is true, auto-fixes are applied for analyzers that support them.
func Run(fix bool) error {
	return RunOnPattern("./...", fix)
}

// RunOnPattern executes all analyzers on packages matching the pattern.
func RunOnPattern(pattern string, fix bool) error {
	cfg := &packages.Config{
		Mode: packages.LoadAllSyntax,
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
	graph, err := checker.Analyze(Analyzers(), pkgs, nil)
	if err != nil {
		return fmt.Errorf("analysis failed: %w", err)
	}

	// Collect diagnostics and apply fixes
	var diagnostics []Diagnostic
	var fixes []fileFix

	for action := range graph.All() {
		if !action.IsRoot {
			continue
		}
		for _, d := range action.Diagnostics {
			pos := action.Package.Fset.Position(d.Pos)

			// Collect fixes from assertlint only
			if fix && action.Analyzer.Name == "assertlint" && len(d.SuggestedFixes) > 0 {
				for _, sf := range d.SuggestedFixes {
					for _, edit := range sf.TextEdits {
						start := action.Package.Fset.Position(edit.Pos)
						end := action.Package.Fset.Position(edit.End)
						fixes = append(fixes, fileFix{
							filename: start.Filename,
							start:    start.Offset,
							end:      end.Offset,
							newText:  edit.NewText,
						})
					}
				}
			} else {
				diagnostics = append(diagnostics, Diagnostic{
					File:    pos.Filename,
					Line:    pos.Line,
					Column:  pos.Column,
					Message: d.Message,
				})
			}
		}
	}

	// Apply fixes
	if len(fixes) > 0 {
		if err := applyFixes(fixes); err != nil {
			return fmt.Errorf("failed to apply fixes: %w", err)
		}
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
	sb.WriteString("vet found issues:\n")
	for _, d := range diagnostics {
		fmt.Fprintf(&sb, "%s:%d:%d: %s\n", d.File, d.Line, d.Column, d.Message)
	}
	return fmt.Errorf("%s", sb.String())
}

type fileFix struct {
	filename string
	start    int
	end      int
	newText  []byte
}

// applyFixes applies text edits to files, grouped by filename.
func applyFixes(fixes []fileFix) error {
	// Group fixes by file
	byFile := make(map[string][]fileFix)
	for _, f := range fixes {
		byFile[f.filename] = append(byFile[f.filename], f)
	}

	for filename, edits := range byFile {
		content, err := os.ReadFile(filename)
		if err != nil {
			return err
		}

		// Sort edits by position (reverse order to apply from end)
		sort.Slice(edits, func(i, j int) bool {
			return edits[i].start > edits[j].start
		})

		// Apply edits from end to start
		for _, edit := range edits {
			content = append(content[:edit.start], append(edit.newText, content[edit.end:]...)...)
		}

		if err := os.WriteFile(filename, content, 0644); err != nil {
			return err
		}
	}

	return nil
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
