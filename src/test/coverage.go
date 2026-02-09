package test

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ICoverageItem is the interface for any coverage entity
type ICoverageItem interface {
	Name() string
	Uncovered() int
	Pct() float32
	ImportPath() string // Returns import path for linking (file path for files/funcs, package path for packages)
	Line() int          // Returns line number (0 for packages/files)
}

// baseCoverageItem provides common coverage stats (embed in concrete types)
type baseCoverageItem struct {
	Covered    int `json:"covered"`
	Statements int `json:"statements"`
}

func (c baseCoverageItem) Uncovered() int { return c.Statements - c.Covered }
func (c baseCoverageItem) Pct() float32 {
	if c.Statements == 0 {
		return 0
	}
	return float32(c.Covered) / float32(c.Statements) * 100
}

// Report is the top-level coverage report for JSON output.
type Report struct {
	Total     float32           `json:"total"`
	Packages  []PackageCoverage `json:"packages"`
	Files     []FileCoverage    `json:"files,omitempty"`
	Functions []FuncCoverage    `json:"funcs,omitempty"`
}

type FuncCoverage struct {
	baseCoverageItem
	FuncLine int           `json:"line"`
	Function string        `json:"function"`
	File     *FileCoverage `json:"-"`
}

func (f FuncCoverage) Name() string       { return fmt.Sprintf(":%d %s", f.FuncLine, f.Function) }
func (f FuncCoverage) ImportPath() string { return f.File.File }
func (f FuncCoverage) Line() int          { return f.FuncLine }

type FileCoverage struct {
	baseCoverageItem
	File      string `json:"file"`
	Functions []FuncCoverage
}

func (f FileCoverage) Name() string {
	if idx := strings.LastIndex(f.File, "/"); idx != -1 {
		return f.File[idx+1:]
	}
	return f.File
}
func (f FileCoverage) ImportPath() string { return f.File }
func (f FileCoverage) Line() int          { return 0 }

type PackageCoverage struct {
	baseCoverageItem
	Package string `json:"package"`
	Files   []FileCoverage
}

func (p PackageCoverage) Name() string       { return p.Package }
func (p PackageCoverage) ImportPath() string { return p.Package }
func (p PackageCoverage) Line() int          { return 0 }

// coverageBlock represents a single block from the coverage profile
type coverageBlock struct {
	file       string
	startLine  int
	endLine    int
	statements int
	count      int
}

// funcInfo represents a function's location in source
type funcInfo struct {
	name      string
	startLine int
	endLine   int
}

// ParseProfile reads a Go coverage profile and returns total coverage and file coverage.
// Each FileCoverage contains its functions with Parent pointers set.
func ParseProfile(filename string) (float32, []FileCoverage, error) {
	blocks, err := parseProfileBlocks(filename)
	if err != nil {
		return 0, nil, err
	}

	// Group blocks by file
	type fileStats struct {
		covered int
		total   int
		blocks  []coverageBlock
	}
	files := make(map[string]*fileStats)

	for _, b := range blocks {
		if files[b.file] == nil {
			files[b.file] = &fileStats{}
		}
		files[b.file].total += b.statements
		if b.count > 0 {
			files[b.file].covered += b.statements
		}
		files[b.file].blocks = append(files[b.file].blocks, b)
	}

	// Calculate totals
	var totalCovered, totalStmts int
	for _, s := range files {
		totalCovered += s.covered
		totalStmts += s.total
	}

	var totalCoverage float32
	if totalStmts > 0 {
		totalCoverage = float32(totalCovered) / float32(totalStmts) * 100
	}

	// Build file coverage list with functions
	var fileCov []FileCoverage
	for name, s := range files {
		fc := FileCoverage{
			baseCoverageItem: baseCoverageItem{
				Statements: s.total,
				Covered:    s.covered,
			},
			File: name,
		}
		// Parse functions and set file pointers
		funcInfos := parseFunctionsFromSource(name)
		fc.Functions = mapBlocksToFuncs(s.blocks, funcInfos)
		for i := range fc.Functions {
			fc.Functions[i].File = &fc
		}
		fileCov = append(fileCov, fc)
	}
	sort.Slice(fileCov, func(i, j int) bool {
		if fileCov[i].Uncovered() != fileCov[j].Uncovered() {
			return fileCov[i].Uncovered() > fileCov[j].Uncovered()
		}
		return fileCov[i].File < fileCov[j].File
	})

	return totalCoverage, fileCov, nil
}

// parseProfileBlocks parses a coverage profile into blocks
func parseProfileBlocks(filename string) ([]coverageBlock, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var blocks []coverageBlock
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "mode:") {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) != 3 {
			continue
		}

		// Format: file:startLine.startCol,endLine.endCol numStmts count
		locPart := parts[0]
		colonIdx := strings.LastIndex(locPart, ":")
		if colonIdx == -1 {
			continue
		}
		filePath := locPart[:colonIdx]
		lineRange := locPart[colonIdx+1:]

		// Parse line range: startLine.startCol,endLine.endCol
		commaIdx := strings.Index(lineRange, ",")
		if commaIdx == -1 {
			continue
		}
		startPart := lineRange[:commaIdx]
		endPart := lineRange[commaIdx+1:]

		startLine := parseLineNum(startPart)
		endLine := parseLineNum(endPart)

		numStmts, _ := strconv.Atoi(parts[1])
		count, _ := strconv.Atoi(parts[2])

		blocks = append(blocks, coverageBlock{
			file:       filePath,
			startLine:  startLine,
			endLine:    endLine,
			statements: numStmts,
			count:      count,
		})
	}

	return blocks, scanner.Err()
}

func parseLineNum(s string) int {
	dotIdx := strings.Index(s, ".")
	if dotIdx != -1 {
		s = s[:dotIdx]
	}
	n, _ := strconv.Atoi(s)
	return n
}

// parseFunctionsFromSource parses a Go source file and returns function locations
func parseFunctionsFromSource(importPath string) []funcInfo {
	// Try to find the source file - importPath is like "github.com/foo/bar/file.go"
	// We need to find it relative to the module
	srcPath := findSourceFile(importPath)
	if srcPath == "" {
		return nil
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, srcPath, nil, 0)
	if err != nil {
		return nil
	}

	var funcs []funcInfo
	ast.Inspect(f, func(n ast.Node) bool {
		switch fn := n.(type) {
		case *ast.FuncDecl:
			start := fset.Position(fn.Pos())
			end := fset.Position(fn.End())
			name := fn.Name.Name
			if fn.Recv != nil && len(fn.Recv.List) > 0 {
				// Method - include receiver type
				name = fmt.Sprintf("%s.%s", receiverType(fn.Recv.List[0].Type), name)
			}
			funcs = append(funcs, funcInfo{
				name:      name,
				startLine: start.Line,
				endLine:   end.Line,
			})
		}
		return true
	})

	return funcs
}

func receiverType(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return receiverType(t.X)
	}
	return ""
}

// findSourceFile tries to locate the source file from an import path
func findSourceFile(importPath string) string {
	// The import path includes the file name, e.g. "github.com/foo/bar/pkg/file.go"
	// We need to find it relative to the current module

	// First, try as a relative path from current directory
	// Strip the module prefix to get relative path
	parts := strings.Split(importPath, "/")
	for i := range parts {
		candidate := filepath.Join(parts[i:]...)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	return ""
}

// mapBlocksToFuncs maps coverage blocks to functions
func mapBlocksToFuncs(blocks []coverageBlock, funcs []funcInfo) []FuncCoverage {
	if len(funcs) == 0 {
		return nil
	}

	// Aggregate stats per function
	type funcStats struct {
		info    funcInfo
		covered int
		total   int
	}
	stats := make(map[string]*funcStats)
	for _, fn := range funcs {
		stats[fn.name] = &funcStats{info: fn}
	}

	// Map each block to its containing function
	for _, b := range blocks {
		for _, fn := range funcs {
			if b.startLine >= fn.startLine && b.endLine <= fn.endLine {
				s := stats[fn.name]
				s.total += b.statements
				if b.count > 0 {
					s.covered += b.statements
				}
				break
			}
		}
	}

	var result []FuncCoverage
	for _, s := range stats {
		if s.total == 0 {
			continue
		}
		result = append(result, FuncCoverage{
			baseCoverageItem: baseCoverageItem{
				Statements: s.total,
				Covered:    s.covered,
			},
			FuncLine: s.info.startLine,
			Function: s.info.name,
		})
	}

	return result
}
