package test

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"gotest.tools/gotestsum/testjson"
	"github.com/wow-look-at-my/go-containers/set"
)

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
	failedTest  set.Set[string]     // tests/packages that failed
	timedOut    set.Set[string]     // tests that timed out
	onOutput    func()              // called before the first visible output
	stderrLines []string            // panics and other stderr noise
	// buildOutput holds compiler/linker diagnostics. `go test -json` reports
	// those as "build-output" events carrying ImportPath and an EMPTY Package,
	// so they match neither the ActionOutput branch below nor any per-package
	// buffer -- which is how a build failure used to print as a bare
	// "FAIL <pkg> [build failed]" with the actual error nowhere on screen.
	buildOutput []string
	testCases   []TestCaseResult // per-test results for CI summary
	timeline    TimelineRecorder // pipeline timeline for per-test spans
	fastCount   int
	fastElapsed float64
}

func (h *coverageHandler) Event(event testjson.TestEvent, exec *testjson.Execution) error {
	// Build diagnostics come first, before any test event: keep them in
	// emission order, and show them live in verbose mode like test output.
	if event.Action == testjson.ActionBuild && event.Output != "" {
		h.buildOutput = append(h.buildOutput, event.Output)
		if h.verbose {
			logger.Output("%s", strings.TrimRight(event.Output, "\n"))
		}
		return nil
	}
	if event.Action == testjson.ActionOutput && event.Output != "" {
		if h.verbose {
			logger.Output("%s", strings.TrimRight(event.Output, "\n"))
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
			h.timedOut.Add(key)
		}
		if matches := coverageRe.FindStringSubmatch(event.Output); len(matches) == 2 {
			cov, _ := strconv.ParseFloat(matches[1], 32)
			h.coverage[event.Package] = float32(cov)
		}
	}
	// Track failed tests and packages
	if event.Action == testjson.ActionFail {
		key := event.Package
		if event.Test != "" {
			key += "/" + event.Test
		}
		h.failedTest.Add(key)
	}

	// Capture per-test results for CI summary
	if event.Test != "" {
		now := time.Now()
		switch event.Action {
		case testjson.ActionPass:
			h.testCases = append(h.testCases, TestCaseResult{
				Package: event.Package, Test: event.Test,
				Status: "pass", Elapsed: event.Elapsed, End: now,
			})
		case testjson.ActionFail:
			h.testCases = append(h.testCases, TestCaseResult{
				Package: event.Package, Test: event.Test,
				Status: "fail", Elapsed: event.Elapsed, End: now,
			})
		case testjson.ActionSkip:
			h.testCases = append(h.testCases, TestCaseResult{
				Package: event.Package, Test: event.Test,
				Status: "skip", Elapsed: event.Elapsed, End: now,
			})
		}

		// Record per-test timeline entries for OTEL trace spans
		if h.timeline != nil && event.Elapsed >= 0.1 {
			switch event.Action {
			case testjson.ActionPass, testjson.ActionFail, testjson.ActionSkip:
				end := time.Now()
				start := end.Add(-time.Duration(event.Elapsed * float64(time.Second)))
				label := shortPkg(event.Package) + "." + event.Test
				h.timeline.Record(label, "test", start, end, event.Action == testjson.ActionFail)
			}
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
			} else {
				h.fastCount++
				h.fastElapsed += event.Elapsed
			}
		case testjson.ActionFail:
			if h.onOutput != nil {
				h.onOutput()
			}
			key := event.Package + "/" + event.Test
			status := "failed!"
			if h.timedOut.Contains(key) {
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
			} else {
				h.fastCount++
				h.fastElapsed += event.Elapsed
			}
		}
	}

	return nil
}

func (h *coverageHandler) printFastSummary() {
	if h.fastCount == 0 || h.verbose {
		return
	}
	if h.onOutput != nil {
		h.onOutput()
	}
	label := "fast tests"
	if h.fastCount == 1 {
		label = "fast test"
	}
	fmt.Fprintf(h.out, "  [%d %s]... %s%.2fs%s\n", h.fastCount, label, colorDimCyan, h.fastElapsed, colorReset)
	h.fastCount = 0
	h.fastElapsed = 0
}

// FailureOutput is everything worth showing about a failed run, in the order
// a reader needs it: what actually went wrong first, the per-test detail after.
// The compiler's own diagnostics lead -- a summary that arrives before the
// error it summarizes is how "[build failed]" becomes the only thing anybody
// sees.
func (h *coverageHandler) FailureOutput() string {
	var result string
	// Build/link diagnostics: the actual error behind "[build failed]".
	for _, line := range h.buildOutput {
		result += line
	}
	// Then stderr (panics and anything the go command wrote outside the
	// JSON stream).
	for _, line := range h.stderrLines {
		result += line + "\n"
	}
	// Then include buffered test/package output for failed items
	for key, lines := range h.testOutput {
		if h.failedTest.Contains(key) {
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
