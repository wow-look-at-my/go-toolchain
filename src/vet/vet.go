package vet

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	git "github.com/go-git/go-git/v5"
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
		RedundantCastAnalyzer,
	}
}

// Run executes all analyzers on the current module.
// If fix is true, auto-fixes are applied for analyzers that support them.
// Returns (filesChanged, error) where filesChanged indicates if any fixes were applied.
// Returns (false, nil) if no go.mod exists (nothing to vet).
func Run(fix bool) (bool, error) {
	if _, err := os.Stat("go.mod"); os.IsNotExist(err) {
		return false, nil
	}
	return RunOnPattern("./...", fix)
}

// RunOnPattern executes all analyzers on packages matching the pattern.
// Returns (filesChanged, error) where filesChanged indicates if any fixes were applied.
func RunOnPattern(pattern string, fix bool) (bool, error) {
	return vetSemantic(pattern, fix)
}

// vetSemantic runs type-aware analysis using go/packages and the analysis framework.
// Returns (filesChanged, error) where filesChanged indicates if any fixes were applied.
func vetSemantic(pattern string, fix bool) (bool, error) {
	filesChanged := false

	// Fix broken testify imports before loading packages
	if fix {
		fixed, err := FixTestifyImports()
		if err != nil {
			return false, fmt.Errorf("fixing testify imports: %w", err)
		}
		if fixed {
			filesChanged = true
		}

		gtFixed, err := MigrateGotestTools()
		if err != nil {
			return false, fmt.Errorf("migrating gotest.tools imports: %w", err)
		}
		if gtFixed {
			filesChanged = true
		}
	}

	cfg := &packages.Config{
		Mode:  packages.LoadAllSyntax,
		Tests: true,
	}

	pkgs, err := packages.Load(cfg, pattern)
	if err != nil {
		return false, fmt.Errorf("failed to load packages: %w", err)
	}

	// Check for load errors
	var loadErrors []string
	for _, pkg := range pkgs {
		for _, e := range pkg.Errors {
			loadErrors = append(loadErrors, e.Error())
		}
	}

	if len(loadErrors) > 0 {
		return false, fmt.Errorf("package load errors:\n%s", strings.Join(loadErrors, "\n"))
	}

	// Run analyzers
	graph, err := checker.Analyze(Analyzers(), pkgs, nil)
	if err != nil {
		return false, fmt.Errorf("analysis failed: %w", err)
	}

	// Collect diagnostics
	var diagnostics []Diagnostic

	for action := range graph.All() {
		if !action.IsRoot {
			continue
		}
		for _, d := range action.Diagnostics {
			pos := action.Package.Fset.Position(d.Pos)
			diagnostics = append(diagnostics, Diagnostic{
				File:    pos.Filename,
				Line:    pos.Line,
				Column:  pos.Column,
				Message: d.Message,
			})
		}

		// Apply AST-based fixes from analyzer results
		if fix {
			if results, ok := action.Result.([]*ASTFixes); ok {
				for _, result := range results {
					if result == nil {
						continue
					}
					if err := checkFileCommitted(result); err != nil {
						return false, err
					}
					if err := result.Apply(); err != nil {
						return false, fmt.Errorf("failed to apply fixes: %w", err)
					}
					filesChanged = true
				}
			}
		}
	}

	// After applying fixes, clean up any side effects (unused vars)
	if filesChanged && fix {
		if _, err := FixUnusedRangeVars("./..."); err != nil {
			return filesChanged, fmt.Errorf("fixing unused range vars: %w", err)
		}
		// Run go mod tidy to add any new dependencies (e.g., testify)
		tidyCmd := exec.Command("go", "mod", "tidy")
		tidyCmd.Stdout = os.Stdout
		tidyCmd.Stderr = os.Stderr
		if err := tidyCmd.Run(); err != nil {
			return filesChanged, fmt.Errorf("go mod tidy failed: %w", err)
		}
		// Re-run analysis to verify fixes worked (don't report old diagnostics)
		_, err := vetSemantic(pattern, fix)
		return true, err
	}

	if len(diagnostics) == 0 {
		return filesChanged, nil
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
	return filesChanged, fmt.Errorf("%s", sb.String())
}

// Diagnostic represents a single analyzer finding.
type Diagnostic struct {
	File    string
	Line    int
	Column  int
	Message string
}

// checkFileCommitted verifies the file is committed before auto-fix modifies it.
func checkFileCommitted(fixes *ASTFixes) error {
	filename := fixes.Fset.Position(fixes.File.Pos()).Filename

	// Open the git repo from the file's directory, not cwd
	fileDir := filepath.Dir(filename)
	repo, err := git.PlainOpenWithOptions(fileDir, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return fmt.Errorf("cannot auto-fix %s: not in a git repo: %w", filename, err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("cannot auto-fix %s: failed to get worktree: %w", filename, err)
	}

	status, err := wt.Status()
	if err != nil {
		return fmt.Errorf("cannot auto-fix %s: failed to get status: %w", filename, err)
	}

	// Get the file path relative to the repo root
	repoRoot := wt.Filesystem.Root()
	relPath, err := filepath.Rel(repoRoot, filename)
	if err != nil {
		return fmt.Errorf("cannot auto-fix %s: failed to get relative path: %w", filename, err)
	}

	if fileStatus, ok := status[relPath]; ok {
		if fileStatus.Staging != git.Unmodified || fileStatus.Worktree != git.Unmodified {
			return fmt.Errorf("cannot auto-fix: %s has uncommitted changes\ncommit or stash changes first", filename)
		}
	}

	return nil
}
