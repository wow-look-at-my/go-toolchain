package test

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/gomod"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"gotest.tools/gotestsum/testjson"
)

// GraphArgFunc, when non-nil, returns an extra flag for the go test argv —
// the build profiler's "-debug-actiongraph=<file>" dump (or "" for none).
// Set by cmd when profiling is enabled; a function hook rather than a direct
// profile import because src/profile depends on src/trace, which reaches
// this package back through src/summary (an import cycle otherwise).
var GraphArgFunc func() string

const (
	clrGreen  = "\033[38;2;0;255;0m"
	clrFail   = "\033[38;2;255;128;128m"
	clrYellow = "\033[38;2;255;255;0m"

	testTimeout = 30 * time.Second
)

// TimelineRecorder records pipeline timeline entries. Satisfied by *summary.Timeline.
type TimelineRecorder interface {
	Record(label, thread string, start, end time.Time, failed bool)
}

var coverageRe = regexp.MustCompile(`coverage: (\d+\.?\d*)% of statements`)

// TestCaseResult captures per-test data for CI summary tables.
type TestCaseResult struct {
	Package string
	Test    string    // includes subtest path, e.g. "TestFoo/case_a"
	Status  string    // "pass", "fail", "skip"
	Elapsed float64   // seconds
	End     time.Time // wall-clock time when the result was received
}

// TestResult contains the results of running tests
type TestResult struct {
	Coverage      Report
	FailureOutput string
	TestCases     []TestCaseResult
}

// readModulePath reads the module path from go.mod in the current directory.
func readModulePath() string {
	return gomod.ReadModulePath()
}

// listTestPackages returns the import paths of packages that contain test files,
// excluding packages where all non-test .go files are generated code (e.g. sqlc).
// It walks the filesystem directly instead of shelling out to `go list`, which
// is significantly faster.
// On any error it returns nil, signaling the caller to fall back to "./...".
func listTestPackages(_ runner.CommandRunner) []string {
	modPath := readModulePath()
	if modPath == "" {
		return nil
	}
	var pkgs []string
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable dirs
		}
		if !d.IsDir() {
			return nil
		}
		// Skip hidden dirs, common non-source dirs, and nested modules
		// (their packages belong to a different module and are not import
		// paths of this one).
		name := d.Name()
		if name != "." && (strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" || gomod.IsNestedModule(path)) {
			return filepath.SkipDir
		}
		// Skip packages where all non-test .go files are generated code
		if isGeneratedPackage(path) {
			return nil
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), "_test.go") {
				rel := filepath.ToSlash(path)
				if rel == "." {
					pkgs = append(pkgs, modPath)
				} else {
					pkgs = append(pkgs, modPath+"/"+rel)
				}
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil
	}
	return pkgs
}

// RunTests executes go test with coverage and returns parsed results.
// coverFile is the path where the coverage profile will be written.
// onOutput is an optional callback called before the first visible test output
// (used by the progress indicator to finish the "..." line).
func RunTests(r runner.CommandRunner, verbose bool, coverFile string, onOutput func(), timeline TimelineRecorder) (*TestResult, error) {
	// Enumerate only packages that have test files to avoid the "no such tool
	// covdata" error on main packages without tests. Also excludes packages
	// where all non-test .go files are generated code (e.g. sqlc output).
	args := []string{"test", "-json", "-timeout=" + testTimeout.String()}
	// Dump the action graph for the build profile. No-op when profiling is
	// off (hook unset — e.g. --no-profile or unit tests).
	if GraphArgFunc != nil {
		if garg := GraphArgFunc(); garg != "" {
			args = append(args, garg)
		}
	}
	if coverFile != "" {
		// -count=1 disables Go's test-result cache for this invocation.
		// Go#74873: when -coverpkg=./... is in play, cached coverprofile
		// fragments reference stale line ranges of packages outside the
		// cached test package. On any edit, the fresh and stale line ranges
		// collide in our dedup map (coverage.go parseProfileBlocks), inflating
		// totals and corrupting aggregate coverage. Compilation is still
		// cached via GOCACHEPROG; only test-result replay is disabled, and
		// only when coverage is being collected.
		args = append(args, "-coverprofile="+coverFile, "-coverpkg=./...", "-count=1")
	}
	if pkgs := listTestPackages(r); len(pkgs) > 0 {
		args = append(args, pkgs...)
	} else {
		args = append(args, "./...")
	}

	// Tee stderr to console (for compilation progress like "go: downloading"
	// and build errors) while also capturing it in a buffer for error reporting.
	var stderrBuf bytes.Buffer
	stderrTee := io.MultiWriter(&stderrBuf, os.Stderr)
	proc, err := runner.Cmd("go", args...).WithStderrWriter(stderrTee).Run(r)
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
		timeline:   timeline,
	}

	execution, err := testjson.ScanTestOutput(testjson.ScanConfig{
		Stdout:                   proc.Stdout(),
		Handler:                  handler,
		IgnoreNonJSONOutputLines: true,
	})
	handler.printFastSummary()
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
				// -o os.DevNull: this is a diagnostic compile to surface the real
				// build error, not a build that should produce a binary. Without
				// -o, `go build <main-pkg>` writes an executable named after the
				// import path's last element into CWD; when that element is "src"
				// (this module's main package) it collides with the src/ directory
				// and fails with "build output \"src\" already exists and is a
				// directory" — which would then mask the very error we are trying
				// to capture. Discard the binary so only the compiler errors show.
				buildProc, buildErr := runner.Cmd("go", "build", "-o", os.DevNull, pkg).WithQuiet().Run(r)
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
	// with no test files). Treat as success.
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
