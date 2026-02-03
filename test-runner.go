package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"

	"gotest.tools/gotestsum/testjson"
)

var coverageRe = regexp.MustCompile(`coverage: (\d+\.?\d*)% of statements`)

// coverageHandler extracts coverage percentages from test output events
type coverageHandler struct {
	coverage     map[string]float32
	verbose      bool
	failureLines []string
}

func (h *coverageHandler) Event(event testjson.TestEvent, exec *testjson.Execution) error {
	if event.Action == testjson.ActionOutput && event.Output != "" {
		if h.verbose {
			fmt.Print(event.Output)
		}
		// Capture failure output for later display
		if !h.verbose && event.Test != "" {
			h.failureLines = append(h.failureLines, event.Output)
		}
		if matches := coverageRe.FindStringSubmatch(event.Output); len(matches) == 2 {
			cov, _ := strconv.ParseFloat(matches[1], 32)
			h.coverage[event.Package] = float32(cov)
		}
	}
	return nil
}

func (h *coverageHandler) FailureOutput() string {
	var result string
	for _, line := range h.failureLines {
		result += line
	}
	return result
}

func (h *coverageHandler) Err(text string) error {
	return nil
}

// TestResult contains the results of running tests
type TestResult struct {
	Packages      []PackageCoverage
	Total         float32
	Files         []FileCoverage
	FailureOutput string
}

// runTests executes go test with coverage and returns parsed results
func runTests(runner CommandRunner, verbose bool) (*TestResult, error) {
	stdout, wait, err := runner.RunWithPipes("go", "test", "-json", "-coverprofile=coverage.out", "./...")
	if err != nil {
		return nil, err
	}

	// Parse test output using testjson
	pkgCoverage := make(map[string]float32)
	handler := &coverageHandler{coverage: pkgCoverage, verbose: verbose}

	execution, err := testjson.ScanTestOutput(testjson.ScanConfig{
		Stdout:  stdout,
		Handler: handler,
	})
	if err != nil {
		return nil, err
	}

	// Capture wait error but continue processing results
	waitErr := wait()

	// Build package results from execution
	var packages []PackageCoverage
	for _, pkgName := range execution.Packages() {
		pkg := execution.Package(pkgName)
		packages = append(packages, PackageCoverage{
			Package:  pkgName,
			Coverage: pkgCoverage[pkgName],
			Passed:   pkg.Result() == testjson.ActionPass,
		})
	}
	sort.Slice(packages, func(i, j int) bool {
		return packages[i].Package < packages[j].Package
	})

	// Parse coverage profile for detailed stats
	totalCoverage, files, err := parseCoverageProfile("coverage.out")
	if err != nil {
		// Fallback to averaging package coverage
		var sum float32
		for _, p := range packages {
			sum += p.Coverage
		}
		if len(packages) > 0 {
			totalCoverage = sum / float32(len(packages))
		}
	}

	return &TestResult{
		Packages:      packages,
		Total:         totalCoverage,
		Files:         files,
		FailureOutput: handler.FailureOutput(),
	}, waitErr
}
