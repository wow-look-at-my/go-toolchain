package vet

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	runtimetrace "runtime/trace"
	"sort"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"
	gotrace "github.com/wow-look-at-my/go-toolchain/src/trace"
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
		AssertNormAnalyzer,
		RedundantCastAnalyzer,
	}
}

// ActiveTrace, if set, receives fine-grained per-file and per-analyzer events.
var ActiveTrace *gotrace.Trace

// ProgressFunc is called with a phase name when the vet enters a new phase.
type ProgressFunc func(phase string)

// Run executes all analyzers on the current module.
// If fix is true, auto-fixes are applied for analyzers that support them.
// Returns (filesChanged, error) where filesChanged indicates if any fixes were applied.
// Returns (false, nil) if no go.mod exists (nothing to vet).
func Run(fix bool) (bool, error) {
	return RunWithProgress(fix, nil)
}

// RunWithProgress is like Run but calls progress with phase names for timing visibility.
func RunWithProgress(fix bool, progress ProgressFunc) (bool, error) {
	if _, err := os.Stat("go.mod"); os.IsNotExist(err) {
		return false, nil
	}
	return RunOnPattern("./...", fix, progress)
}

// RunOnPattern executes all analyzers on packages matching the pattern.
// Returns (filesChanged, error) where filesChanged indicates if any fixes were applied.
func RunOnPattern(pattern string, fix bool, progress ProgressFunc) (bool, error) {
	return vetSemantic(pattern, fix, progress)
}

// vetSemantic runs type-aware analysis using go/packages and the analysis framework.
// Returns (filesChanged, error) where filesChanged indicates if any fixes were applied.
func vetSemantic(pattern string, fix bool, progress ProgressFunc) (bool, error) {
	filesChanged := false

	report := func(phase string) {
		if progress != nil {
			progress(phase)
		}
	}

	// Fix broken testify imports before loading packages
	if fix {
		report("fix imports")
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

	// Load packages for analysis.
	report("type-check")
	cfg := &packages.Config{
		Mode:  packages.LoadAllSyntax,
		Tests: true,
	}
	var nParsed int
	cfg.ParseFile = func(fset *token.FileSet, filename string, src []byte) (*ast.File, error) {
		nParsed++
		_, task := runtimetrace.NewTask(context.Background(), "parse/"+filepath.Base(filename))
		f, err := parser.ParseFile(fset, filename, src, parser.AllErrors|parser.ParseComments)
		task.End()
		return f, err
	}

	loadStart := time.Now()
	pkgs, err := packages.Load(cfg, pattern)
	loadDur := time.Since(loadStart)
	if err != nil {
		return false, fmt.Errorf("failed to load packages: %w", err)
	}

	if progress != nil {
		var nPkgs int
		packages.Visit(pkgs, func(p *packages.Package) bool {
			nPkgs++
			return true
		}, nil)
		fmt.Fprintf(os.Stderr, "vet: loaded %d packages (%d files parsed) in %v\n", nPkgs, nParsed, loadDur.Round(time.Millisecond))
	}

	// Check for load errors, filtering out Go version mismatch warnings.
	// These occur when go-toolchain was built with an older Go than the target
	// project declares in go.mod. The embedded go/packages can still analyze
	// the code correctly — the go directive is a minimum version, not a syntax gate.
	var loadErrors []string
	for _, pkg := range pkgs {
		for _, e := range pkg.Errors {
			msg := e.Error()
			if strings.Contains(msg, "package requires newer Go version") ||
				strings.Contains(msg, "source-processing packages") {
				continue
			}
			loadErrors = append(loadErrors, msg)
		}
	}

	if len(loadErrors) > 0 {
		return false, fmt.Errorf("package load errors:\n%s", strings.Join(loadErrors, "\n"))
	}

	// Run analyzers — wrap each Run function to record per-analyzer per-package timing.
	report("run analyzers")
	analyzers := Analyzers()
	analyzers = instrumentAnalyzers(analyzers)
	graph, err := checker.Analyze(analyzers, pkgs, nil)
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
		_, err := vetSemantic(pattern, fix, progress)
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

// instrumentAnalyzers wraps each analyzer's Run function in-place to record
// per-analyzer per-package timing in the trace. Mutates the original analyzers
// since cloning breaks checker.Analyze's internal pointer-identity maps.
func instrumentAnalyzers(analyzers []*analysis.Analyzer) []*analysis.Analyzer {
	seen := make(map[*analysis.Analyzer]bool)
	var instrument func(a *analysis.Analyzer)
	instrument = func(a *analysis.Analyzer) {
		if seen[a] {
			return
		}
		seen[a] = true
		origRun := a.Run
		name := a.Name
		a.Run = func(pass *analysis.Pass) (interface{}, error) {
			_, task := runtimetrace.NewTask(context.Background(), "analyze/"+name+"/"+pass.Pkg.Path())
			result, err := origRun(pass)
			task.End()
			return result, err
		}
		for _, req := range a.Requires {
			instrument(req)
		}
	}
	for _, a := range analyzers {
		instrument(a)
	}
	return analyzers
}

// Diagnostic represents a single analyzer finding.
type Diagnostic struct {
	File    string
	Line    int
	Column  int
	Message string
}


// checkFileCommitted verifies the file is committed before auto-fix modifies it.
// It tries go-git first, falling back to shelling out to git if go-git fails
// for infrastructure reasons (e.g., unsupported repo format, worktree bugs).
func checkFileCommitted(fixes *ASTFixes) error {
	filename := fixes.Fset.Position(fixes.File.Pos()).Filename

	err := checkFileCommittedGoGit(filename)
	if err == nil {
		return nil
	}
	// If go-git detected uncommitted changes, trust that result
	if strings.Contains(err.Error(), "uncommitted changes") {
		return err
	}
	// go-git failed for infrastructure reasons; fall back to git CLI
	return checkFileCommittedExec(filename)
}

// checkFileCommittedGoGit checks file status using the go-git library.
func checkFileCommittedGoGit(filename string) error {
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

// checkFileCommittedExec checks file status by shelling out to the git CLI.
// Used as a fallback when go-git encounters bugs or unsupported repo features.
func checkFileCommittedExec(filename string) error {
	cmd := exec.Command("git", "status", "--porcelain", "--", filename)
	cmd.Dir = filepath.Dir(filename)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("cannot auto-fix %s: git status failed: %w", filename, err)
	}
	if len(strings.TrimSpace(string(out))) > 0 {
		return fmt.Errorf("cannot auto-fix: %s has uncommitted changes\ncommit or stash changes first", filename)
	}
	return nil
}
