// Package lint implements a Go analyzer that detects near-duplicate code
// blocks and suggests refactoring. It uses linearized AST comparison
// with longest-common-subsequence similarity scoring.
//
// The analyzer integrates with go vet and golangci-lint via the standard
// golang.org/x/tools/go/analysis framework.
package lint

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"

	"golang.org/x/tools/go/analysis"
)

// Default configuration values.
const (
	DefaultThreshold = 0.85
	DefaultMinNodes  = 20
)

// Analyzer is the go/analysis entry point for the duplicate-code linter.
var Analyzer = &analysis.Analyzer{
	Name: "dupcode",
	Doc:  "detects near-duplicate code blocks and suggests refactoring",
	Run:  run,
}

func init() {
	Analyzer.Flags.Float64("threshold", DefaultThreshold,
		"minimum similarity score (0.0-1.0) to report as duplicate")
	Analyzer.Flags.Int("min-nodes", DefaultMinNodes,
		"minimum AST node count for a block to be considered")
}

func run(pass *analysis.Pass) (interface{}, error) {
	threshold := DefaultThreshold
	minNodes := DefaultMinNodes

	if f := pass.Analyzer.Flags.Lookup("threshold"); f != nil {
		if v, err := strconv.ParseFloat(f.Value.String(), 64); err == nil {
			threshold = v
		}
	}
	if f := pass.Analyzer.Flags.Lookup("min-nodes"); f != nil {
		if v, err := strconv.Atoi(f.Value.String()); err == nil {
			minNodes = v
		}
	}

	// Collect blocks from all files in the package.
	fileBlocks := make(map[string][]Block)
	for _, file := range pass.Files {
		filename := pass.Fset.Position(file.Pos()).Filename
		blocks := ExtractBlocks(file, pass.Fset, minNodes)
		if len(blocks) > 0 {
			fileBlocks[filename] = blocks
		}
	}

	pairs := FindDuplicates(fileBlocks, threshold)

	for _, pair := range pairs {
		suggestion := BuildSuggestion(pair)
		posA := pass.Fset.Position(pair.A.Pos)
		posB := pass.Fset.Position(pair.B.Pos)

		msg := fmt.Sprintf(
			"near-duplicate code (%.0f%% similar): %s (%s:%d) and %s (%s:%d). %s",
			pair.Similarity*100,
			pair.A.FuncName, posA.Filename, posA.Line,
			pair.B.FuncName, posB.Filename, posB.Line,
			suggestion.Description,
		)

		pass.Report(analysis.Diagnostic{
			Pos:     pair.A.Pos,
			End:     pair.A.End,
			Message: msg,
			SuggestedFixes: []analysis.SuggestedFix{
				{
					Message: suggestion.Description,
				},
			},
		})
	}

	return nil, nil
}

// RunOnFiles is a standalone entry point (not using go/analysis Pass)
// for running the linter on parsed files. Returns duplicate pairs with
// suggestions. This is used by the CLI subcommand.
func RunOnFiles(files map[string]*ast.File, fset *token.FileSet, threshold float64, minNodes int) []DuplicateReport {
	fileBlocks := make(map[string][]Block)
	for filename, file := range files {
		blocks := ExtractBlocks(file, fset, minNodes)
		if len(blocks) > 0 {
			fileBlocks[filename] = blocks
		}
	}

	pairs := FindDuplicates(fileBlocks, threshold)

	var reports []DuplicateReport
	for _, pair := range pairs {
		suggestion := BuildSuggestion(pair)
		posA := fset.Position(pair.A.Pos)
		posB := fset.Position(pair.B.Pos)
		reports = append(reports, DuplicateReport{
			FuncA:      pair.A.FuncName,
			FuncB:      pair.B.FuncName,
			FileA:      posA.Filename,
			FileB:      posB.Filename,
			LineA:      posA.Line,
			LineB:      posB.Line,
			Similarity: pair.Similarity,
			Suggestion: suggestion,
		})
	}
	return reports
}

// DuplicateReport is a serializable result for CLI output.
type DuplicateReport struct {
	FuncA      string     `json:"func_a"`
	FuncB      string     `json:"func_b"`
	FileA      string     `json:"file_a"`
	FileB      string     `json:"file_b"`
	LineA      int        `json:"line_a"`
	LineB      int        `json:"line_b"`
	Similarity float64    `json:"similarity"`
	Suggestion Suggestion `json:"suggestion"`
}
