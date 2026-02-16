package vet

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	git "github.com/go-git/go-git/v5"
	ansi "github.com/wow-look-at-my/ansi-writer"
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
	// if err := vetSyntax(pattern, fix); err != nil {
	// 	return err
	// }
	return vetSemantic(pattern, fix)
}

// vetSyntax runs syntax-only checks using go/parser (no compilation required).
// This handles things like unused imports that would block packages.Load.
// Only used as fallback if vetSemantic can't handle them.
func vetSyntax(pattern string, fix bool) error {
	if !fix {
		return nil
	}

	fixed, err := FixUnusedImports(pattern)
	if err != nil {
		return fmt.Errorf("fixing unused imports: %w", err)
	}
	for _, f := range fixed {
		fmt.Printf("\033[33mfixed unused import: %s\033[0m\n", f)
	}
	return nil
}

// isUnusedImportError checks if an error is an "imported and not used" error.
func isUnusedImportError(errMsg string) bool {
	return strings.Contains(errMsg, "imported and not used")
}

// vetSemantic runs type-aware analysis using go/packages and the analysis framework.
func vetSemantic(pattern string, fix bool) error {
	cfg := &packages.Config{
		Mode:  packages.LoadAllSyntax,
		Tests: true,
	}

	pkgs, err := packages.Load(cfg, pattern)
	if err != nil {
		return fmt.Errorf("failed to load packages: %w", err)
	}

	// Check for load errors - separate unused import errors from others
	var loadErrors []string
	var unusedImportErrors []string
	for _, pkg := range pkgs {
		for _, e := range pkg.Errors {
			if isUnusedImportError(e.Error()) {
				unusedImportErrors = append(unusedImportErrors, e.Error())
			} else {
				loadErrors = append(loadErrors, e.Error())
			}
		}
	}

	// If there are non-import errors, fail
	if len(loadErrors) > 0 {
		return fmt.Errorf("package load errors:\n%s", strings.Join(loadErrors, "\n"))
	}

	// If there are unused import errors, fix them using the loaded AST and re-run
	if len(unusedImportErrors) > 0 {
		if !fix {
			return fmt.Errorf("package load errors:\n%s", strings.Join(unusedImportErrors, "\n"))
		}

		// Find fixes using the already-loaded AST
		importFixes := FindUnusedImportFixes(pkgs)
		if len(importFixes) > 0 {
			if err := applyFixes(importFixes); err != nil {
				return fmt.Errorf("failed to fix unused imports: %w", err)
			}
			// Re-run semantic analysis with fixed files
			return vetSemantic(pattern, fix)
		}
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

			// Collect fixes (only our custom analyzers provide SuggestedFixes)
			if fix && len(d.SuggestedFixes) > 0 {
				for _, sf := range d.SuggestedFixes {
					for _, edit := range sf.TextEdits {
						start := action.Package.Fset.Position(edit.Pos)
						end := action.Package.Fset.Position(edit.End)
						fixes = append(fixes, fileFix{
							loc:     SourceLocation{File: start.Filename, Line: pos.Line, Column: pos.Column},
							start:   start.Offset,
							end:     end.Offset,
							newText: edit.NewText,
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

// SourceLocation identifies a position in source code.
type SourceLocation struct {
	File   string
	Line   int
	Column int
}

// ShortLoc returns "relative/path:line" for display.
func (s SourceLocation) ShortLoc() string {
	cwd, _ := os.Getwd()
	relPath := s.File
	if rel, err := filepath.Rel(cwd, s.File); err == nil {
		relPath = rel
	}
	return fmt.Sprintf("%s:%d", relPath, s.Line)
}

type fileFix struct {
	loc     SourceLocation
	start   int
	end     int
	oldText []byte
	newText []byte
}

// printFix prints a fix message showing old (red) and new (green) text.
func printFix(f fileFix) {
	fileURL := fmt.Sprintf("file://%s:%d", f.loc.File, f.loc.Line)
	yellow := ansi.Style("fixed:", ansi.Yellow.FG())
	grey := ansi.Link(fileURL, ansi.Style(f.loc.ShortLoc(), ansi.BrightBlack.FG()))

	// Format old text (red)
	oldStr := strings.TrimSpace(string(f.oldText))
	if idx := strings.Index(oldStr, "\n"); idx > 0 {
		oldStr = oldStr[:idx] + "..."
	}
	red := ansi.Style(oldStr, ansi.Red.FG())

	// Format output based on whether this is a deletion or replacement
	newStr := strings.TrimSpace(string(f.newText))
	if newStr == "" {
		// Deletion: just show -oldText
		fmt.Printf("%s %s -%s\n", yellow, grey, red)
	} else {
		// Replacement: show old → new
		if idx := strings.Index(newStr, "\n"); idx > 0 {
			newStr = newStr[:idx] + "..."
		}
		green := ansi.Style(newStr, ansi.Green.FG())
		fmt.Printf("%s %s %s → %s\n", yellow, grey, red, green)
	}
}

// applyFixes applies text edits to files, grouped by filename.
// It prints each fix showing old → new text.
func applyFixes(fixes []fileFix) error {
	// Group fixes by file
	byFile := make(map[string][]*fileFix)
	for i := range fixes {
		byFile[fixes[i].loc.File] = append(byFile[fixes[i].loc.File], &fixes[i])
	}

	// Check that all files are committed before modifying
	if err := checkFilesCommitted(byFile); err != nil {
		return err
	}

	for filename, edits := range byFile {
		content, err := os.ReadFile(filename)
		if err != nil {
			return err
		}

		// Capture old text for each edit
		for _, edit := range edits {
			if edit.start < len(content) && edit.end <= len(content) {
				edit.oldText = content[edit.start:edit.end]
			}
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

		// Print all fixes for this file
		for _, edit := range edits {
			printFix(*edit)
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

// checkFilesCommitted verifies all files are committed before auto-fix modifies them.
func checkFilesCommitted(byFile map[string][]*fileFix) error {
	repo, err := git.PlainOpenWithOptions(".", &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		// Not a git repo - skip check
		return nil
	}

	wt, err := repo.Worktree()
	if err != nil {
		return nil
	}

	status, err := wt.Status()
	if err != nil {
		return nil
	}

	// Check if any of the target files have changes
	var dirty []string
	for f := range byFile {
		if fileStatus, ok := status[f]; ok {
			if fileStatus.Staging != git.Unmodified || fileStatus.Worktree != git.Unmodified {
				dirty = append(dirty, f)
			}
		}
	}

	if len(dirty) > 0 {
		sort.Strings(dirty)
		return fmt.Errorf("cannot auto-fix: files have uncommitted changes\n%s\ncommit or stash changes first", strings.Join(dirty, "\n"))
	}

	return nil
}
