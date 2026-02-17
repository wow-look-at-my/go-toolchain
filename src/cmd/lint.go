package cmd

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-toolchain/src/lint"
)

func init() {
	lintCmd := &cobra.Command{
		Use:   "lint [packages/directories...]",
		Short: "Detect near-duplicate code blocks",
		Long: `Scans Go source files for near-duplicate function bodies using
linearized AST comparison with longest-common-subsequence scoring.

Reports pairs of functions whose structural similarity exceeds the
threshold and suggests refactoring by identifying the varying concrete
values that would become parameters of an extracted function.

This check also runs automatically during builds (warnings only).
Use --dupcode=false to disable it during builds.

Examples:
  go-toolchain lint ./...
  go-toolchain lint --threshold 0.90 ./cmd ./internal
  go-toolchain lint --json ./...`,
		SilenceUsage: true,
		RunE:         runLint,
	}

	rootCmd.AddCommand(lintCmd)
}

func runLint(cmd *cobra.Command, args []string) error {
	return runLintImpl(args)
}

func runLintImpl(args []string) error {
	if len(args) == 0 {
		args = []string{"./..."}
	}

	fset := token.NewFileSet()
	allFiles := make(map[string]*ast.File)

	for _, pattern := range args {
		paths, err := resolveGoFiles(pattern)
		if err != nil {
			return fmt.Errorf("resolving %s: %w", pattern, err)
		}
		for _, path := range paths {
			if _, exists := allFiles[path]; exists {
				continue
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", path, err)
				continue
			}
			allFiles[path] = f
		}
	}

	if len(allFiles) == 0 {
		if !jsonOutput {
			fmt.Println("No Go files found.")
		}
		return nil
	}

	reports := lint.RunOnFiles(allFiles, fset, lintThreshold, lintMinNodes)

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(reports)
	}

	if len(reports) == 0 {
		fmt.Println("No near-duplicate code blocks found.")
		return nil
	}

	fmt.Printf("Found %d near-duplicate pair(s):\n\n", len(reports))
	for i, r := range reports {
		fmt.Printf("%s%d. %.0f%% similar:%s\n", colorYellow, i+1, r.Similarity*100, colorReset)
		fmt.Printf("   %s:%d  function %s%s%s\n", r.FileA, r.LineA, colorGreen, r.FuncA, colorReset)
		fmt.Printf("   %s:%d  function %s%s%s\n", r.FileB, r.LineB, colorGreen, r.FuncB, colorReset)
		fmt.Printf("   %s\n\n", r.Suggestion.Description)
	}

	return nil
}

// resolveGoFiles expands a pattern (directory, ./..., or file) into
// a list of .go file paths (excluding test files).
func resolveGoFiles(pattern string) ([]string, error) {
	// Handle ./... recursive pattern
	if strings.HasSuffix(pattern, "/...") {
		root := strings.TrimSuffix(pattern, "/...")
		if root == "." || root == "" {
			root = "."
		}
		return walkGoFiles(root)
	}

	// Check if it's a directory
	info, err := os.Stat(pattern)
	if err != nil {
		// Try as a file path
		if strings.HasSuffix(pattern, ".go") {
			return []string{pattern}, nil
		}
		return nil, err
	}

	if info.IsDir() {
		return listGoFilesInDir(pattern)
	}

	if strings.HasSuffix(pattern, ".go") {
		return []string{pattern}, nil
	}

	return nil, nil
}

func walkGoFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			// Skip hidden dirs, vendor, testdata
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func listGoFilesInDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	return files, nil
}
