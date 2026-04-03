package test

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"
	"sort"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"gotest.tools/gotestsum/testjson"
)

const (
	clrGreen  = "\033[38;2;0;255;0m"
	clrFail   = "\033[38;2;255;128;128m"
	clrYellow = "\033[38;2;255;255;0m"

	testTimeout = 30 * time.Second
)

var coverageRe = regexp.MustCompile(`coverage: (\d+\.?\d*)% of statements`)

// shortPkg returns the last path segment of a package name.
func shortPkg(pkg string) string {
	if i := strings.LastIndex(pkg, "/"); i >= 0 {
		return pkg[i+1:]
	}
	return pkg
}

// coverageHandler extracts coverage percentages from test output events
type coverageHandler struct {
	coverage    map[string]float32
	verbose     bool
	out         io.Writer
	testOutput  map[string][]string // buffer output per test/package until we know pass/fail
	failedTest  map[string]bool     // tests/packages that failed
	timedOut    map[string]bool     // tests that timed out
	onOutput    func()              // called before the first visible output
	stderrLines []string            // build errors and panics from stderr
	testCases   []TestCaseResult    // per-test results for CI summary
}

func (h *coverageHandler) Event(event testjson.TestEvent, exec *testjson.Execution) error {
	if event.Action == testjson.ActionOutput && event.Output != "" {
		if h.verbose {
			fmt.Print(event.Output)
		}
		// Buffer output per-test/package for later (if test/package fails)
		if !h.verbose && h.testOutput != nil {
			key := event.Package
			if event.Test != "" {
				key += "/" + event.Test
			}
			h.testOutput[key] = append(h.testOutput[key], event.Output)
		}
		// Detect test timeout from panic output
		if event.Test != "" && strings.Contains(event.Output, "panic: test timed out") {
			key := event.Package + "/" + event.Test
			h.timedOut[key] = true
		}
		if matches := coverageRe.FindStringSubmatch(event.Output); len(matches) == 2 {
			cov, _ := strconv.ParseFloat(matches[1], 32)
			h.coverage[event.Package] = float32(cov)
		}
	}
	// Track failed tests and packages
	if event.Action == testjson.ActionFail && h.failedTest != nil {
		key := event.Package
		if event.Test != "" {
			key += "/" + event.Test
		}
		h.failedTest[key] = true
	}

	// Capture per-test results for CI summary
	if event.Test != "" {
		switch event.Action {
		case testjson.ActionPass:
			h.testCases = append(h.testCases, TestCaseResult{
				Package: event.Package, Test: event.Test,
				Status: "pass", Elapsed: event.Elapsed,
			})
		case testjson.ActionFail:
			h.testCases = append(h.testCases, TestCaseResult{
				Package: event.Package, Test: event.Test,
				Status: "fail", Elapsed: event.Elapsed,
			})
		case testjson.ActionSkip:
			h.testCases = append(h.testCases, TestCaseResult{
				Package: event.Package, Test: event.Test,
				Status: "skip", Elapsed: event.Elapsed,
			})
		}
	}

	// Real-time test status (non-verbose only)
	if !h.verbose && event.Test != "" {
		pkg := shortPkg(event.Package)
		switch event.Action {
		case testjson.ActionPass:
			if event.Elapsed >= 0.1 {
				if h.onOutput != nil {
					h.onOutput()
				}
				fmt.Fprintf(h.out, "  %s.%s... %sdone.%s %s%.2fs%s\n", pkg, event.Test, clrGreen, colorReset, colorDimCyan, event.Elapsed, colorReset)
			}
		case testjson.ActionFail:
			if h.onOutput != nil {
				h.onOutput()
			}
			key := event.Package + "/" + event.Test
			status := "failed!"
			if h.timedOut[key] {
				status = "timed out!"
			}
			elapsed := event.Elapsed
			if elapsed < 0 {
				elapsed = testTimeout.Seconds()
			}
			fmt.Fprintf(h.out, "  %s.%s... %s%s%s %s%.2fs%s\n", pkg, event.Test, clrFail, status, colorReset, colorDimCyan, elapsed, colorReset)
		case testjson.ActionSkip:
			if event.Elapsed >= 0.1 {
				if h.onOutput != nil {
					h.onOutput()
				}
				fmt.Fprintf(h.out, "  %s.%s... %sskipped.%s %s%.2fs%s\n", pkg, event.Test, clrYellow, colorReset, colorDimCyan, event.Elapsed, colorReset)
			}
		}
	}

	return nil
}

func (h *coverageHandler) FailureOutput() string {
	var result string
	// Include stderr (build errors, panics) first
	for _, line := range h.stderrLines {
		result += line + "\n"
	}
	// Then include buffered test/package output for failed items
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
	h.stderrLines = append(h.stderrLines, text)
	return nil
}

// TestCaseResult captures per-test data for CI summary tables.
type TestCaseResult struct {
	Package string
	Test    string  // includes subtest path, e.g. "TestFoo/case_a"
	Status  string  // "pass", "fail", "skip"
	Elapsed float64 // seconds
}

// TestResult contains the results of running tests
type TestResult struct {
	Coverage      Report
	FailureOutput string
	TestCases     []TestCaseResult
}

// listTestPackages returns the import paths of packages that contain test files.
// On any error it returns nil, signaling the caller to fall back to "./...".
func listTestPackages(r runner.CommandRunner) []string {
	proc, err := runner.Cmd("go", "list", "-f",
		`{{if .TestGoFiles}}{{.ImportPath}}{{end}}`, "./...").WithQuiet().Run(r)
	if err != nil {
		return nil
	}
	out, _ := io.ReadAll(proc.Stdout())
	if proc.Wait() != nil {
		return nil
	}
	var pkgs []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			pkgs = append(pkgs, line)
		}
	}
	return pkgs
}

// RunTests executes go test with coverage and returns parsed results.
// coverFile is the path where the coverage profile will be written.
// goMinor is the resolved Go minor version (e.g. 25 for Go 1.25); pass 0 to
// use the legacy ./... behavior.
// onOutput is an optional callback called before the first visible test output
// (used by the progress indicator to finish the "..." line).
func RunTests(r runner.CommandRunner, verbose bool, coverFile string, goMinor int, onOutput func()) (*TestResult, error) {
	// Build the go test argument list. For Go 1.25+, enumerate only packages
	// that have test files to avoid the "no such tool covdata" error on main
	// packages without tests. Also add -coverpkg=./... so that code in non-test
	// packages exercised by tests elsewhere is counted toward coverage.
	args := []string{"test", "-vet=off", "-json", "-timeout=" + testTimeout.String(), "-coverprofile=" + coverFile}
	if goMinor >= 25 {
		if testPkgs := listTestPackages(r); len(testPkgs) > 0 {
			args = append(args, "-coverpkg=./...")
			args = append(args, testPkgs...)
		} else {
			args = append(args, "./...") // fallback
		}
	} else {
		args = append(args, "./...")
	}

	// Capture stderr in a buffer — build errors go here, not in JSON stream.
	var stderrBuf bytes.Buffer
	proc, err := runner.Cmd("go", args...).WithStderrWriter(&stderrBuf).Run(r)
	if err != nil {
		return nil, err
	}

	// Parse test output using testjson
	pkgCoverage := make(map[string]float32)
	handler := &coverageHandler{
		coverage:   pkgCoverage,
		verbose:    verbose,
		out:        os.Stdout,
		testOutput: make(map[string][]string),
		failedTest: make(map[string]bool),
		timedOut:   make(map[string]bool),
		onOutput:   onOutput,
	}

	execution, err := testjson.ScanTestOutput(testjson.ScanConfig{
		Stdout:                   proc.Stdout(),
		Handler:                  handler,
		IgnoreNonJSONOutputLines: true,
	})
	if err != nil {
		return nil, err
	}

	// Capture wait error but continue processing results.
	// Wait() drains stderr into stderrBuf via WithStderrWriter.
	waitErr := proc.Wait()

	// Include captured stderr (build errors) in handler output.
	// Filter out noise that is not actual build/test errors:
	//  - "no such tool covdata" from Go 1.25+ coverage on main packages
	//  - "# pkg" header lines that precede filtered errors
	//  - "cacheprog:" messages from GOCACHEPROG subprocesses
	if stderrBuf.Len() > 0 {
		var filtered []string
		for _, line := range strings.Split(strings.TrimRight(stderrBuf.String(), "\n"), "\n") {
			if strings.Contains(line, "no such tool") && strings.Contains(line, "covdata") {
				continue
			}
			if strings.HasPrefix(line, "# ") {
				continue
			}
			if strings.HasPrefix(line, "cacheprog:") {
				continue
			}
			filtered = append(filtered, line)
		}
		if len(filtered) > 0 {
			handler.stderrLines = append(handler.stderrLines, strings.Join(filtered, "\n"))
		}
	}

	// If packages failed to build but no error details were captured (common
	// with CGO packages where compiler errors bypass the JSON stream), re-run
	// a plain go build to capture the actual compiler errors.
	if waitErr != nil && len(handler.failedTest) > 0 {
		hasDetails := false
		for _, lines := range handler.stderrLines {
			if strings.Contains(lines, ":") {
				hasDetails = true
				break
			}
		}
		if !hasDetails {
			// Pick any failing package to get the build error.
			// failedTest keys may include test names (pkg/TestFoo);
			// strip the test suffix to get a valid package path.
			for key := range handler.failedTest {
				pkg := key
				if i := strings.LastIndex(pkg, "/"); i > 0 {
					// Only strip if the suffix looks like a test name (starts with uppercase).
					if suffix := pkg[i+1:]; len(suffix) > 0 && suffix[0] >= 'A' && suffix[0] <= 'Z' {
						pkg = pkg[:i]
					}
				}
				buildProc, buildErr := runner.Cmd("go", "build", pkg).WithQuiet().Run(r)
				if buildErr != nil {
					break
				}
				var buildStderr bytes.Buffer
				io.Copy(&buildStderr, buildProc.Stderr())
				buildProc.Wait()
				if buildStderr.Len() > 0 {
					handler.stderrLines = append(handler.stderrLines, strings.TrimRight(buildStderr.String(), "\n"))
					break
				}
			}
		}
	}

	// If go test failed and no tests ran, check if there's failure output
	// (e.g., compilation errors) before falling back to a generic message.
	if waitErr != nil && execution.Total() == 0 {
		failOutput := handler.FailureOutput()
		if failOutput != "" {
			return &TestResult{FailureOutput: failOutput}, waitErr
		}
		return nil, fmt.Errorf("no tests found (create *_test.go files with Test* functions)")
	}

	// If go test exited non-zero but no actual tests failed, the failure is
	// from a non-test issue (e.g. missing "covdata" tool on a main package
	// with no test files in Go 1.25+). Treat as success.
	if waitErr != nil && len(handler.failedTest) == 0 && handler.FailureOutput() == "" {
		waitErr = nil
	}

	// Determine reachable packages to filter coverage (non-fatal on error)
	reachable, _ := ReachablePackages(r)

	// Parse coverage profile for total and file coverage (files contain functions)
	totalCoverage, files, _ := ParseProfileFiltered(coverFile, reachable)

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
		// Skip packages not in the reachable import graph
		if reachable != nil && !reachable[pkgName] {
			continue
		}
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
		TestCases:     handler.testCases,
	}, waitErr
}
