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
	"sync"
	"time"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/buildtags"
	gotrace "github.com/wow-look-at-my/go-toolchain/src/trace"
	"golang.org/x/tools/go/analysis"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/packages"
)

// Analyzers returns the custom analyzers to run in-process. Standard go vet
// analyzers already run via go test's built-in -vet flag; only checks go
// vet does not know about run here.
func Analyzers() []*analysis.Analyzer {
	return []*analysis.Analyzer{
		AssertLintAnalyzer,
		AssertNormAnalyzer,
		DeadCodeAnalyzer,
		BannedOutputAnalyzer,
		CommentNumbersAnalyzer,
		CommentSpanAnalyzer,
		JSONInterpAnalyzer,
		MapSetAnalyzer,
		RedundantCastAnalyzer,
		SliceSetAnalyzer,
		TestifyCastAnalyzer,
		WriteRunsAnalyzer,
	}
}

// ActiveTrace, if set, receives fine-grained per-file and per-analyzer events.
var ActiveTrace *gotrace.Trace

// ProgressFunc is called with a phase name when the vet enters a new phase.
type ProgressFunc func(phase string)

// Run executes all analyzers on the current module. If fix is true, auto-fixes
// are applied. Returns (false, nil) if no go.mod exists.
func Run(fix bool) (bool, error) {
	return RunWithProgress(fix, nil)
}

// RunWithProgress is like Run but calls progress with phase names for timing visibility.
func RunWithProgress(fix bool, progress ProgressFunc) (bool, error) {
	if _, err := os.Stat("go.mod"); os.IsNotExist(err) {
		return false, nil
	}
	// ed applies fixes locally or records violations for CI; gofmt and the semantic pass share it.
	ed := NewEditor(fix)
	if progress != nil {
		progress("gofmt")
	}
	fmtChanged, err := RunGofmt(ed)
	if err != nil {
		return false, err
	}
	semanticChanged, err := vetSemantic("./...", ed, progress)
	return fmtChanged || semanticChanged, err
}

// RunFromSource type-checks every dependency from SOURCE. The default reads
// export data, which is faster and can be rejected. Depth: docs/CI.md
func RunFromSource(fix bool, progress ProgressFunc) (bool, error) {
	loadDepsFromSource = true
	defer func() { loadDepsFromSource = false }()
	return RunWithProgress(fix, progress)
}

// loadDepsFromSource is set only for the duration of RunFromSource.
var loadDepsFromSource bool

// NeedModule populates pkg.Module, which bannedoutput scopes its ban by.
// NeedDeps drops export data, so no importer is in the path.
func loadMode() packages.LoadMode {
	mode := packages.LoadSyntax | packages.NeedModule
	if loadDepsFromSource {
		mode |= packages.NeedDeps
	}
	return mode
}

// RunOnPattern executes all analyzers on packages matching pattern.
// Returns whether any fixes were applied.
func RunOnPattern(pattern string, fix bool, progress ProgressFunc) (bool, error) {
	return vetSemantic(pattern, NewEditor(fix), progress)
}

// loadErrorMessages collects load errors from the WHOLE import graph, not just
// the roots: a failed dependency records its cause on its own Errors, and the
// root only carries the downstream `undefined:` cascade. Go version mismatch
// warnings are dropped (a minimum, not a syntax gate). Messages are
// deduplicated: a directory's test variants carry the same Errors.
func loadErrorMessages(pkgs []*packages.Package) []string {
	var msgs []string
	seen := set.New[string]()
	packages.Visit(pkgs, func(pkg *packages.Package) bool {
		for _, e := range pkg.Errors {
			msg := e.Error()
			if strings.Contains(msg, "package requires newer Go version") ||
				strings.Contains(msg, "source-processing packages") {
				continue
			}
			if !seen.Add(msg) {
				continue
			}
			msgs = append(msgs, msg)
		}
		return true
	}, nil)
	return msgs
}

// vetSemantic runs type-aware analysis using go/packages and the analysis
// framework, routing every fix through ed (apply locally / report on CI).
// Returns (filesChanged, error) where filesChanged indicates if any fixes were
// written (only possible with a fix-mode editor).
func vetSemantic(pattern string, ed Editor, progress ProgressFunc) (bool, error) {
	filesChanged := false

	report := func(phase string) {
		if progress != nil {
			progress(phase)
		}
	}

	// Migrates testify/gotest.tools imports up front, so CI fails on old-fork usage instead of passing green.
	report("fix imports")
	fixed, err := FixTestifyImports(ed)
	if err != nil {
		return false, fmt.Errorf("fixing testify imports: %w", err)
	}
	if fixed {
		filesChanged = true
	}

	gtFixed, err := MigrateGotestTools(ed)
	if err != nil {
		return false, fmt.Errorf("migrating gotest.tools imports: %w", err)
	}
	if gtFixed {
		filesChanged = true
	}

	// On CI, surface recorded import/gofmt violations before the type-check.
	// No-op locally: a fix-mode editor never records violations.
	if e := ed.Err(); e != nil {
		return filesChanged, e
	}

	// Every build-tag config the module needs; buildtags.Verify below proves none was missed.
	resetJSONInterpWarnings()
	resetMapSetWarnings()
	resetSliceSetWarnings()
	resetWriteRunWarnings()
	resetCommentSpanWarnings()
	resetCommentNumbersWarnings()
	discovery, err := buildtags.Scan(".")
	if err != nil {
		return filesChanged, fmt.Errorf("discovering build tags: %w", err)
	}
	analyzedFiles := set.New[string]()
	var diagnostics []Diagnostic
	var nParsed int

	for i, tagCfg := range discovery.Configs {
		// The default config covers the whole module; extras need only gated directories.
		pat := []string{pattern}
		if i > 0 {
			pat = discovery.GatedPatterns()
			if len(pat) == 0 {
				continue
			}
		}
		changed, err := vetOneConfig(pat, tagCfg, ed, report, &diagnostics, analyzedFiles, &nParsed)
		if changed {
			filesChanged = true
		}
		if err != nil {
			return filesChanged, err
		}
		// A fix rewrote the tree; the caller re-runs the whole vet, so stop
		// here rather than analyzing later configurations against stale ASTs.
		if filesChanged {
			break
		}
	}

	if missed := buildtags.Verify(discovery, analyzedFiles); len(missed) > 0 {
		return filesChanged, buildtags.UnreachableError(missed, "vet")
	}

	return finishSemantic(pattern, ed, progress, filesChanged, diagnostics)
}

// vetOneConfig loads and analyzes the module under a single build-tag
// configuration, appending diagnostics and recording every file it actually
// parsed into analyzedFiles (module-relative, slash separated) so Verify can
// prove no tagged file went unseen.
func vetOneConfig(patterns []string, tagCfg buildtags.Config, ed Editor, report func(string),
	diagnostics *[]Diagnostic, analyzedFiles set.Set[string], nParsedTotal *int,
) (bool, error) {
	filesChanged := false

	report("type-check " + tagCfg.String())
	cfg := &packages.Config{
		Mode:  loadMode(),
		Tests: true,
	}
	if arg := tagCfg.Arg(); arg != "" {
		cfg.BuildFlags = []string{"-tags", arg}
	}
	rec := &parseRecorder{files: analyzedFiles, root: moduleRoot()}
	cfg.ParseFile = func(fset *token.FileSet, filename string, src []byte) (*ast.File, error) {
		rec.record(filename)
		_, task := runtimetrace.NewTask(context.Background(), "parse/"+filepath.Base(filename))
		f, err := parser.ParseFile(fset, filename, src, parser.AllErrors|parser.ParseComments)
		task.End()
		return f, err
	}

	loadStart := time.Now()
	pkgs, err := packages.Load(cfg, patterns...)
	loadDur := time.Since(loadStart)
	if err != nil {
		return false, fmt.Errorf("failed to load packages: %w", err)
	}

	var nPkgs int
	packages.Visit(pkgs, func(p *packages.Package) bool {
		nPkgs++
		return true
	}, nil)
	nParsed := rec.count()
	*nParsedTotal += nParsed
	logger.Info("vet: loaded %d packages (%d files parsed) under tags %s in %v",
		nPkgs, nParsed, tagCfg, loadDur.Round(time.Millisecond))

	loadErrors := loadErrorMessages(pkgs)

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

	deadCodeVariant := richestVariants(graph, DeadCodeAnalyzer.Name)

	for action := range graph.All() {
		if !action.IsRoot {
			continue
		}
		// "Unused" must mean the package WITH its tests, or a test-only helper looks
		// dead. Only the richest variant of each path reports.
		if action.Analyzer.Name == DeadCodeAnalyzer.Name &&
			deadCodeVariant[action.Package.PkgPath] != action.Package.ID {
			continue
		}
		for _, d := range action.Diagnostics {
			pos := action.Package.Fset.Position(d.Pos)
			*diagnostics = append(*diagnostics, Diagnostic{
				File:    pos.Filename,
				Line:    pos.Line,
				Column:  pos.Column,
				Message: d.Message,
			})
		}

		// Route analyzer fixes through the editor: locally it writes them, on CI
		// it records a violation (testifycast, which has no diagnostic of its own)
		// or relies on the analyzer's own diagnostic (AST fixes). No CI branch
		// here — the editor decides apply vs report.
		if results, ok := action.Result.([]*ASTFixes); ok {
			for _, result := range results {
				if result == nil {
					continue
				}
				// The guard protects the user's edits: skip it where there are none.
				name := fixesFilename(result)
				if ed.Writes() && !ed.Wrote(name) {
					if err := checkFileCommitted(result); err != nil {
						return false, err
					}
				}
				wrote, err := result.Apply(ed)
				if err != nil {
					return false, fmt.Errorf("failed to apply fixes: %w", err)
				}
				if wrote {
					filesChanged = true
				}
			}
		}
		// Surgical byte-edit fixes (testifycast conversions).
		if results, ok := action.Result.([]*CastEdits); ok {
			for _, result := range results {
				if result == nil || len(result.Edits) == 0 {
					continue
				}
				if ed.Writes() && !ed.Wrote(result.Filename) {
					if err := checkFileCommittedByName(result.Filename); err != nil {
						return false, err
					}
				}
				wrote, err := result.Apply(ed)
				if err != nil {
					return false, fmt.Errorf("failed to apply cast edits: %w", err)
				}
				if wrote {
					filesChanged = true
				}
			}
		}
	}

	return filesChanged, nil
}

// finishSemantic applies the post-analysis steps a single time, after every build-tag
// configuration has run: re-run on a rewritten tree, then render the collected
// diagnostics and editor violations.
func finishSemantic(pattern string, ed Editor, progress ProgressFunc,
	filesChanged bool, diagnostics []Diagnostic,
) (bool, error) {
	// After applying fixes, clean up any side effects (unused vars). filesChanged
	// is only ever true for a fix-mode editor (a check editor never writes), so
	// this whole block is local-only without testing the CI flag.
	if filesChanged {
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
		_, err := vetSemantic(pattern, ed, progress)
		return true, err
	}

	// Combines analyzer diagnostics with editor violations recorded during analysis (CI mode).
	var msgs []string
	if ve := ed.Err(); ve != nil {
		msgs = append(msgs, ve.Error())
	}
	if len(diagnostics) > 0 {
		sort.Slice(diagnostics, func(i, j int) bool {
			if diagnostics[i].File != diagnostics[j].File {
				return diagnostics[i].File < diagnostics[j].File
			}
			return diagnostics[i].Line < diagnostics[j].Line
		})
		var sb strings.Builder
		sb.WriteString("vet found issues:\n")
		for _, d := range diagnostics {
			fmt.Fprintf(&sb, "%s:%d:%d: %s\n", d.File, d.Line, d.Column, d.Message)
		}
		msgs = append(msgs, strings.TrimRight(sb.String(), "\n"))
	}
	if len(msgs) > 0 {
		return filesChanged, fmt.Errorf("%s", strings.Join(msgs, "\n"))
	}
	return filesChanged, nil
}

// instrumentedAnalyzers tracks which *analysis.Analyzer pointers already
// carry the trace wrapper, across every call in the process. The analyzers
// are package-level singletons shared by every vet run in this binary, so a
// call-local set is not enough: a second run would wrap the already-wrapped
// Run func again, and each further run adds one more layer — unbounded
// nesting that eventually overflows the stack.
var instrumentedAnalyzers sync.Map // *analysis.Analyzer -> struct{}

// instrumentAnalyzers wraps each analyzer's Run function in-place to record
// per-analyzer per-package timing in the trace. Mutates the original analyzers
// since cloning breaks checker.Analyze's internal pointer-identity maps. Safe
// to call more than once on the same analyzer: a re-wrap is skipped.
func instrumentAnalyzers(analyzers []*analysis.Analyzer) []*analysis.Analyzer {
	seen := set.New[*analysis.Analyzer]()
	var instrument func(a *analysis.Analyzer)
	instrument = func(a *analysis.Analyzer) {
		if !seen.Add(a) {
			return
		}
		if _, already := instrumentedAnalyzers.LoadOrStore(a, struct{}{}); !already {
			origRun := a.Run
			name := a.Name
			a.Run = func(pass *analysis.Pass) (interface{}, error) {
				_, task := runtimetrace.NewTask(context.Background(), "analyze/"+name+"/"+pass.Pkg.Path())
				result, err := origRun(pass)
				task.End()
				return result, err
			}
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
func checkFileCommitted(fixes *ASTFixes) error {
	return checkFileCommittedByName(fixesFilename(fixes))
}

// fixesFilename names the file these AST fixes rewrite.
func fixesFilename(fixes *ASTFixes) string {
	return fixes.Fset.Position(fixes.File.Pos()).Filename
}

// checkFileCommittedByName is checkFileCommitted keyed by an explicit filename,
// used by fix producers that don't carry an *ASTFixes (e.g. cast text edits).
// It tries go-git, then falls back to the git CLI on infrastructure errors.
func checkFileCommittedByName(filename string) error {
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

// moduleRoot is the module's absolute path, resolved a single time so per-file
// coverage records can key on module-relative paths.
var moduleRootOnce struct {
	sync.Once
	path string
}

func moduleRoot() string {
	moduleRootOnce.Do(func() {
		if wd, err := os.Getwd(); err == nil {
			moduleRootOnce.path = wd
		}
	})
	return moduleRootOnce.path
}
