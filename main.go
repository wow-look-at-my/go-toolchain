package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// TestEvent matches go test -json output format
type TestEvent struct {
	Time    string  `json:"Time,omitempty"`
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test,omitempty"`
	Elapsed float64 `json:"Elapsed,omitempty"`
	Output  string  `json:"Output,omitempty"`
}

type CoverageReport struct {
	Total    float64           `json:"total"`
	Packages []PackageCoverage `json:"packages"`
	Files    []FileCoverage    `json:"files,omitempty"`
	Funcs    []FuncCoverage    `json:"funcs,omitempty"`
}

type PackageCoverage struct {
	Package  string  `json:"package"`
	Coverage float64 `json:"coverage"`
	Passed   bool    `json:"passed"`
}

type FileCoverage struct {
	File       string  `json:"file"`
	Coverage   float64 `json:"coverage"`
	Statements int     `json:"statements"`
	Covered    int     `json:"covered"`
}

type FuncCoverage struct {
	File     string  `json:"file"`
	Line     int     `json:"line"`
	Function string  `json:"function"`
	Coverage float64 `json:"coverage"`
}

var coverageRe = regexp.MustCompile(`coverage: (\d+\.?\d*)% of statements`)

var (
	covDetail   string
	minCoverage float64
	output      string
	jsonOutput  bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "go-safe-build",
		Short: "Build Go projects with coverage enforcement",
		Long:  "Runs go mod tidy, go test with coverage, and go build. Fails if coverage is below threshold.",
		RunE:  run,
	}

	rootCmd.Flags().StringVar(&covDetail, "cov-detail", "", "Show detailed coverage: 'func' or 'file'")
	rootCmd.Flags().Float64Var(&minCoverage, "min-coverage", 80.0, "Minimum coverage percentage (0 = test only, no build)")
	rootCmd.Flags().StringVarP(&output, "output", "o", "", "Output binary name (default: directory name)")
	rootCmd.Flags().BoolVar(&jsonOutput, "json", false, "Output coverage report as JSON")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	testOnly := minCoverage == 0

	if !jsonOutput {
		fmt.Println("==> go mod tidy")
	}
	if err := runCmd(jsonOutput, "go", "mod", "tidy"); err != nil {
		return fmt.Errorf("go mod tidy failed: %w", err)
	}

	if !jsonOutput {
		fmt.Println("==> go test -json -coverprofile=coverage.out ./...")
	}

	testCmd := exec.Command("go", "test", "-json", "-coverprofile=coverage.out", "./...")
	stdout, err := testCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}
	testCmd.Stderr = os.Stderr

	if err := testCmd.Start(); err != nil {
		return fmt.Errorf("failed to start go test: %w", err)
	}

	// Parse JSON events
	var packages []PackageCoverage
	pkgCoverage := make(map[string]float64)
	pkgPassed := make(map[string]bool)

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		var event TestEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}

		if event.Action == "output" && event.Output != "" {
			if matches := coverageRe.FindStringSubmatch(event.Output); len(matches) == 2 {
				cov, _ := strconv.ParseFloat(matches[1], 64)
				pkgCoverage[event.Package] = cov
			}
		}

		if event.Action == "pass" && event.Test == "" {
			pkgPassed[event.Package] = true
		}
		if event.Action == "fail" && event.Test == "" {
			pkgPassed[event.Package] = false
		}
	}

	if err := testCmd.Wait(); err != nil {
		return fmt.Errorf("go test failed: %w", err)
	}

	for pkg, cov := range pkgCoverage {
		packages = append(packages, PackageCoverage{
			Package:  pkg,
			Coverage: cov,
			Passed:   pkgPassed[pkg],
		})
	}
	sort.Slice(packages, func(i, j int) bool {
		return packages[i].Package < packages[j].Package
	})

	totalCoverage, files, err := parseCoverageProfile("coverage.out")
	if err != nil {
		var sum float64
		for _, p := range packages {
			sum += p.Coverage
		}
		if len(packages) > 0 {
			totalCoverage = sum / float64(len(packages))
		}
	}

	report := &CoverageReport{
		Total:    totalCoverage,
		Packages: packages,
	}

	if covDetail == "file" && len(files) > 0 {
		report.Files = files
	}

	if covDetail == "func" {
		funcs, _ := parseFuncCoverage("coverage.out")
		report.Funcs = funcs
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "\t")
		if err := enc.Encode(report); err != nil {
			return fmt.Errorf("failed to encode JSON: %w", err)
		}
	} else {
		fmt.Println("\n==> Package coverage:")
		for _, p := range packages {
			status := "PASS"
			if !p.Passed {
				status = "FAIL"
			}
			fmt.Printf("  %6.1f%%  [%s]  %s\n", p.Coverage, status, p.Package)
		}

		if covDetail == "file" && len(report.Files) > 0 {
			fmt.Println("\n==> File coverage:")
			for _, f := range report.Files {
				fmt.Printf("  %6.1f%%  %s\n", f.Coverage, f.File)
			}
		}

		if covDetail == "func" && len(report.Funcs) > 0 {
			fmt.Println("\n==> Function coverage:")
			for _, f := range report.Funcs {
				fmt.Printf("  %6.1f%%  %s:%d %s\n", f.Coverage, f.File, f.Line, f.Function)
			}
		}

		fmt.Printf("\n==> Total coverage: %.1f%%\n", report.Total)
	}

	// Test-only mode: report coverage and exit with error
	if testOnly {
		if !jsonOutput {
			fmt.Println("\n==> Test-only mode (--min-coverage 0), skipping build")
		}
		return fmt.Errorf("test-only mode: coverage is %.1f%%", report.Total)
	}

	if report.Total < minCoverage {
		return fmt.Errorf("coverage %.1f%% is below minimum %.1f%%", report.Total, minCoverage)
	}

	if output == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get working directory: %w", err)
		}
		output = filepath.Base(wd)
	}

	if !jsonOutput {
		fmt.Printf("==> go build -o %s ./...\n", output)
	}
	if err := runCmd(jsonOutput, "go", "build", "-o", output, "./..."); err != nil {
		return fmt.Errorf("go build failed: %w", err)
	}

	if !jsonOutput {
		fmt.Println("==> Build successful")
	}
	return nil
}

func runCmd(quiet bool, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if !quiet {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}

func parseCoverageProfile(filename string) (float64, []FileCoverage, error) {
	file, err := os.Open(filename)
	if err != nil {
		return 0, nil, err
	}
	defer file.Close()

	type stats struct {
		covered int
		total   int
	}
	fileStats := make(map[string]*stats)

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

		locPart := parts[0]
		colonIdx := strings.LastIndex(locPart, ":")
		if colonIdx == -1 {
			continue
		}
		filePath := locPart[:colonIdx]

		numStmts, _ := strconv.Atoi(parts[1])
		count, _ := strconv.Atoi(parts[2])

		if fileStats[filePath] == nil {
			fileStats[filePath] = &stats{}
		}
		fileStats[filePath].total += numStmts
		if count > 0 {
			fileStats[filePath].covered += numStmts
		}
	}

	var totalCovered, totalStmts int
	for _, s := range fileStats {
		totalCovered += s.covered
		totalStmts += s.total
	}

	var totalCoverage float64
	if totalStmts > 0 {
		totalCoverage = float64(totalCovered) / float64(totalStmts) * 100
	}

	var files []FileCoverage
	for name, s := range fileStats {
		var cov float64
		if s.total > 0 {
			cov = float64(s.covered) / float64(s.total) * 100
		}
		files = append(files, FileCoverage{
			File:       name,
			Coverage:   cov,
			Statements: s.total,
			Covered:    s.covered,
		})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].File < files[j].File
	})

	return totalCoverage, files, nil
}

func parseFuncCoverage(coverageFile string) ([]FuncCoverage, error) {
	cmd := exec.Command("go", "tool", "cover", "-func", coverageFile)
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var funcs []FuncCoverage
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "total:") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		covStr := strings.TrimSuffix(fields[len(fields)-1], "%")
		cov, _ := strconv.ParseFloat(covStr, 64)

		fileLine := fields[0]
		colonIdx := strings.LastIndex(fileLine, ":")
		if colonIdx == -1 {
			continue
		}
		file := fileLine[:colonIdx]
		lineNum, _ := strconv.Atoi(fileLine[colonIdx+1:])

		funcs = append(funcs, FuncCoverage{
			File:     file,
			Line:     lineNum,
			Function: fields[1],
			Coverage: cov,
		})
	}

	return funcs, nil
}
