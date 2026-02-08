package test

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"

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

	// Build package results from execution
	var packages []PackageCoverage
	for _, pkgName := range execution.Packages() {
		pkg := execution.Package(pkgName)
		cov, hasStatements := pkgCoverage[pkgName]
		packages = append(packages, PackageCoverage{
			Package:      pkgName,
			Coverage:     cov,
			Passed:       pkg.Result() == testjson.ActionPass,
			NoStatements: !hasStatements,
		})
	}
	// Sort by coverage ascending (lowest first) so the most
	// under-covered packages are visible at the top.
	// Packages with no statements sort to the end.
	sort.Slice(packages, func(i, j int) bool {
		if packages[i].NoStatements != packages[j].NoStatements {
			return !packages[i].NoStatements
		}
		if packages[i].Coverage != packages[j].Coverage {
			return packages[i].Coverage < packages[j].Coverage
		}
		return packages[i].Package < packages[j].Package
	})

	// Parse coverage profile for total coverage computed as
	// (total covered statements) / (total statements) across all files.
	totalCoverage, files, _ := ParseProfile(coverFile)

	// Parse function-level coverage
	funcs, _ := ParseFuncCoverage(coverFile)

	return &TestResult{
		Coverage: Report{
			Total:    totalCoverage,
			Packages: packages,
			Files:    files,
			Funcs:    funcs,
		},
		FailureOutput: handler.FailureOutput(),
	}, waitErr
}
