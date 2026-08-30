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

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/buildtags"
	"github.com/wow-look-at-my/go-toolchain/src/gomod"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"gotest.tools/gotestsum/testjson"
)

// GraphArgFunc returns an extra go test flag when profiling is on; a hook avoids an import cycle through src/profile.
var GraphArgFunc func() string

const (
	clrGreen  = "\033[38;2;0;255;0m"
	clrFail   = "\033[38;2;255;128;128m"
	clrYellow = "\033[38;2;255;255;0m"

	// Bounds the run, and must clear the SLOWEST host: the windows leg killed src/cmd and src/vet here.
	testTimeout = 2 * time.Minute
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
		// Skip hidden dirs, non-source dirs, and nested modules (different module, not our import paths).
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

// RunTests executes go test with coverage under EVERY build-tag configuration
// the module needs, and returns the merged results.
//
// Running only the default configuration meant a test behind `//go:build
// sometag` never compiled and never ran, so it could not fail -- a bypass by
// omission. The tag sets come from buildtags.Scan, and verifyTagCoverage then
// PROVES every gated file was compiled by some configuration; an unreachable file
// fails the run rather than being skipped.
//
// coverFile is the path where the coverage profile will be written.
// onOutput is an optional callback called before any visible test output
// (used by the progress indicator to finish the "..." line).
func RunTests(r runner.CommandRunner, verbose bool, coverFile string, onOutput func(), timeline TimelineRecorder) (*TestResult, error) {
	discovery, err := buildtags.Scan(".")
	if err != nil {
		return nil, fmt.Errorf("discovering build tags: %w", err)
	}

	var merged *TestResult
	var firstErr error
	for i, tagCfg := range discovery.Configs {
		// Coverage is collected only on the default config; extra configs still run and can fail, just uncovered.
		cf := coverFile
		cb := onOutput
		var only []string
		if i > 0 {
			cf, cb = "", nil
			only = discovery.GatedPatterns()
			if len(only) == 0 {
				continue
			}
			logger.Info("tests: build tags %s (%s)", tagCfg, strings.Join(only, " "))
		}
		res, err := runTestsOnce(r, verbose, cf, cb, timeline, tagCfg, only)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		merged = mergeTestResults(merged, res)
	}
	if firstErr != nil {
		return merged, firstErr
	}

	if err := verifyTagCoverage(r, discovery); err != nil {
		return merged, err
	}
	return merged, nil
}

// mergeTestResults folds a configuration's results into the accumulator,
// keeping the default configuration's coverage report (the only report collected).
func mergeTestResults(acc, next *TestResult) *TestResult {
	if next == nil {
		return acc
	}
	if acc == nil {
		return next
	}
	acc.TestCases = append(acc.TestCases, next.TestCases...)
	if next.FailureOutput != "" {
		if acc.FailureOutput != "" {
			acc.FailureOutput += "\n"
		}
		acc.FailureOutput += next.FailureOutput
	}
	return acc
}

// verifyTagCoverage asks the go tool which files each configuration actually
// builds, and fails when a build-tagged file was compiled by none of them. This
// is the guarantee that a tag cannot hide a test: the check is on the real file
// set the toolchain saw, not on the enumeration that produced the tag sets.
func verifyTagCoverage(r runner.CommandRunner, d *buildtags.Discovery) error {
	if len(d.Gated) == 0 {
		return nil
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolving module root: %w", err)
	}
	seen := set.New[string]()
	for _, tagCfg := range d.Configs {
		args := []string{"list", "-e",
			"-f", "{{$d := .Dir}}{{range .GoFiles}}{{$d}}/{{.}}\n{{end}}" +
				"{{range .TestGoFiles}}{{$d}}/{{.}}\n{{end}}" +
				"{{range .XTestGoFiles}}{{$d}}/{{.}}\n{{end}}" +
				"{{range .IgnoredGoFiles}}{{end}}"}
		if arg := tagCfg.Arg(); arg != "" {
			args = append(args, "-tags", arg)
		}
		args = append(args, "./...")
		proc, err := runner.Cmd("go", args...).WithHostTarget().WithQuiet().Run(r)
		if err != nil {
			return fmt.Errorf("listing files for tags %s: %w", tagCfg, err)
		}
		var out bytes.Buffer
		io.Copy(&out, proc.Stdout())
		proc.Wait()
		for _, line := range strings.Split(out.String(), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if rel, err := filepath.Rel(root, line); err == nil && !strings.HasPrefix(rel, "..") {
				seen.Add(filepath.ToSlash(rel))
			}
		}
	}
	// Empty means the check itself didn't run, not that every file is unreachable.
	if seen.IsEmpty() {
		logger.Warn("tests: skipped the build-tag reachability check -- `go list` reported no files at all, so nothing could be verified")
		return nil
	}
	if missed := buildtags.Verify(d, seen); len(missed) > 0 {
		return buildtags.UnreachableError(missed, "tests")
	}
	return nil
}

// runTestsOnce executes go test for a single build-tag configuration.
func runTestsOnce(r runner.CommandRunner, verbose bool, coverFile string, onOutput func(),
	timeline TimelineRecorder, tagCfg buildtags.Config, only []string,
) (*TestResult, error) {
	// Enumerate only packages with test files, avoiding the "no such tool covdata" error and generated-only packages.
	args := []string{"test", "-json", "-timeout=" + testTimeout.String()}
	if arg := tagCfg.Arg(); arg != "" {
		args = append(args, "-tags", arg)
	}
	// Dump the action graph for the build profile. No-op when profiling is
	// off (hook unset — e.g. --no-profile or unit tests).
	if GraphArgFunc != nil {
		if garg := GraphArgFunc(); garg != "" {
			args = append(args, garg)
		}
	}
	if coverFile != "" {
		// -count disables result caching only; stale coverprofile fragments otherwise corrupt coverage (https://go.dev/issue/74873).
		args = append(args, "-coverprofile="+coverFile, "-coverpkg=./...", "-count=1")
	}
	switch {
	case len(only) > 0:
		args = append(args, only...)
	default:
		if pkgs := listTestPackages(r); len(pkgs) > 0 {
			args = append(args, pkgs...)
		} else {
			args = append(args, "./...")
		}
	}

	// Tee stderr to console and a buffer, for progress and error reporting.
	var stderrBuf bytes.Buffer
	stderrTee := io.MultiWriter(&stderrBuf, os.Stderr)
	proc, err := runner.Cmd("go", args...).WithHostTarget().WithStderrWriter(stderrTee).Run(r)
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
		failedTest: set.New[string](),
		timedOut:   set.New[string](),
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

	// Wait() drains stderr into stderrBuf via WithStderrWriter while capturing the result.
	waitErr := proc.Wait()

	// Include captured stderr (build errors) in handler output.
	// Filter out noise that is not actual build/test errors:
	//  - "no such tool covdata" from recent Go coverage on main packages
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
	if waitErr != nil && !handler.failedTest.IsEmpty() {
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
			for key := range handler.failedTest.All() {
				pkg := key
				if i := strings.LastIndex(pkg, "/"); i > 0 {
					// Only strip if the suffix looks like a test name (starts with uppercase).
					if suffix := pkg[i+1:]; len(suffix) > 0 && suffix[0] >= 'A' && suffix[0] <= 'Z' {
						pkg = pkg[:i]
					}
				}
				// -o discards the binary; without it, `go build src` would write an executable colliding with the src/ directory.
				buildProc, buildErr := runner.Cmd("go", "build", "-o", os.DevNull, pkg).WithHostTarget().WithQuiet().Run(r)
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

	// A failing exit code with no failed test is a non-test issue (e.g. missing
	// "covdata" on a main package with no tests); treat it as success.
	if waitErr != nil && handler.failedTest.IsEmpty() && handler.FailureOutput() == "" {
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
		if !reachable.IsEmpty() && !reachable.Contains(pkgName) {
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

	// Sort by uncovered statements, the most uncovered at the top
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
