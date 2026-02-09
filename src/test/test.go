package test

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gotest.tools/gotestsum/testjson"
)

// CommandRunner abstracts command execution for testing
type CommandRunner interface {
	Run(name string, args ...string) error
	RunWithOutput(name string, args ...string) ([]byte, error)
	RunWithPipes(name string, args ...string) (stdout io.Reader, wait func() error, err error)
}

var coverageRe = regexp.MustCompile(`coverage: (\d+\.?\d*)% of statements`)

// coverageHandler extracts coverage percentages from test output events
type coverageHandler struct {
	coverage   map[string]float32
	verbose    bool
	testOutput map[string][]string // buffer output per test until we know pass/fail
	failedTest map[string]bool     // tests that failed
}

func (h *coverageHandler) Event(event testjson.TestEvent, exec *testjson.Execution) error {
	if event.Action == testjson.ActionOutput && event.Output != "" {
		if h.verbose {
			fmt.Print(event.Output)
		}
		// Buffer output per-test for later (if test fails)
		if !h.verbose && event.Test != "" {
			key := event.Package + "/" + event.Test
			h.testOutput[key] = append(h.testOutput[key], event.Output)
		}
		if matches := coverageRe.FindStringSubmatch(event.Output); len(matches) == 2 {
			cov, _ := strconv.ParseFloat(matches[1], 32)
			h.coverage[event.Package] = float32(cov)
		}
	}
	// Track failed tests
	if event.Action == testjson.ActionFail && event.Test != "" {
		key := event.Package + "/" + event.Test
		h.failedTest[key] = true
	}
	return nil
}

func (h *coverageHandler) FailureOutput() string {
	var result string
	for key, lines := range h.testOutput {
		if h.failedTest[key] {
			for _, line := range lines {
				result += line
			}
		}
	}
	return result
}

func (h *coverageHandler) Err(text string) error {
	return nil
}

// TestResult contains the results of running tests
type TestResult struct {
	Coverage      Report
	FailureOutput string
}

// RunTests executes go test with coverage and returns parsed results.
// coverFile is the path where the coverage profile will be written.
func RunTests(runner CommandRunner, verbose bool, coverFile string) (*TestResult, error) {
	stdout, wait, err := runner.RunWithPipes("go", "test", "-json", "-coverprofile="+coverFile, "./...")
	if err != nil {
		return nil, err
	}

	// Parse test output using testjson
	pkgCoverage := make(map[string]float32)
	handler := &coverageHandler{
		coverage:   pkgCoverage,
		verbose:    verbose,
		testOutput: make(map[string][]string),
		failedTest: make(map[string]bool),
	}

	execution, err := testjson.ScanTestOutput(testjson.ScanConfig{
		Stdout:  stdout,
		Handler: handler,
	})
	if err != nil {
		return nil, err
	}

	// Capture wait error but continue processing results
	waitErr := wait()

	// Parse coverage profile for total and file coverage (files contain functions)
	totalCoverage, files, _ := ParseProfile(coverFile)

	// Group files by package path
	pkgFiles := make(map[string][]FileCoverage)
	for _, f := range files {
		// Extract package path from file path (everything before last /)
		pkgPath := f.File
		if idx := strings.LastIndex(f.File, "/"); idx != -1 {
			pkgPath = f.File[:idx]
		}
		pkgFiles[pkgPath] = append(pkgFiles[pkgPath], f)
	}

	// Build package results from execution
	var packages []PackageCoverage
	for _, pkgName := range execution.Packages() {
		p := PackageCoverage{
			Package: pkgName,
		}
		// Find matching package files (match by suffix since pkgName is full import path)
		for path, pf := range pkgFiles {
			if strings.HasSuffix(pkgName, path) || strings.HasSuffix(path, pkgName) || path == pkgName {
				p.Files = pf
				for _, f := range pf {
					p.Statements += f.Statements
					p.Covered += f.Covered
				}
				break
			}
		}
		packages = append(packages, p)
	}

	// Sort by uncovered statements (most uncovered first)
	sort.Slice(packages, func(i, j int) bool {
		if packages[i].Uncovered() != packages[j].Uncovered() {
			return packages[i].Uncovered() > packages[j].Uncovered()
		}
		return packages[i].Package < packages[j].Package
	})

	return &TestResult{
		Coverage: Report{
			Total:    totalCoverage,
			Packages: packages,
			Files:    files,
		},
		FailureOutput: handler.FailureOutput(),
	}, waitErr
}
