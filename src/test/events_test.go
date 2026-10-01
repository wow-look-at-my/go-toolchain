package test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-containers/set"
	"gotest.tools/gotestsum/testjson"
)

func TestCoverageHandlerExtractsCoverage(t *testing.T) {
	t.Serial()
	h := &coverageHandler{coverage: make(map[string]float32)}

	// Simulate output event with coverage info
	event := testjson.TestEvent{
		Action:  testjson.ActionOutput,
		Package: "example.com/pkg",
		Output:  "coverage: 75.5% of statements\n",
	}

	require.NoError(t, h.Event(event, nil))

	assert.Equal(t, float32(75.5), h.coverage["example.com/pkg"])
}

func TestCoverageHandlerIgnoresNonCoverageOutput(t *testing.T) {
	t.Serial()
	h := &coverageHandler{coverage: make(map[string]float32)}

	event := testjson.TestEvent{
		Action:  testjson.ActionOutput,
		Package: "example.com/pkg",
		Output:  "=== RUN TestFoo\n",
	}

	require.NoError(t, h.Event(event, nil))

	_, exists := h.coverage["example.com/pkg"]
	assert.False(t, exists)
}

func TestCoverageHandlerIgnoresNonOutputActions(t *testing.T) {
	t.Serial()
	h := &coverageHandler{coverage: make(map[string]float32)}

	event := testjson.TestEvent{
		Action:  testjson.ActionPass,
		Package: "example.com/pkg",
	}

	require.NoError(t, h.Event(event, nil))

	_, exists := h.coverage["example.com/pkg"]
	assert.False(t, exists)
}

func TestCoverageHandlerErr(t *testing.T) {
	t.Serial()
	h := &coverageHandler{coverage: make(map[string]float32)}
	assert.NoError(t, h.Err("some error"))
}

func TestCoverageRegex(t *testing.T) {
	t.Serial()
	tests := []struct {
		input    string
		expected string
	}{
		{"coverage: 80.0% of statements", "80.0"},
		{"coverage: 100% of statements", "100"},
		{"coverage: 0.0% of statements", "0.0"},
		{"coverage: 45.5% of statements", "45.5"},
		{"no coverage here", ""},
	}

	for _, tc := range tests {
		matches := coverageRe.FindStringSubmatch(tc.input)
		if tc.expected == "" {
			assert.LessOrEqual(t, len(matches), 0)
		} else {
			require.Equal(t, 2, len(matches), "input %q: expected match, got %v", tc.input, matches)
			assert.Equal(t, tc.expected, matches[1], "input %q", tc.input)
		}
	}
}

func TestCoverageHandlerMultiplePackages(t *testing.T) {
	t.Serial()
	h := &coverageHandler{coverage: make(map[string]float32)}

	events := []testjson.TestEvent{
		{Action: testjson.ActionOutput, Package: "pkg1", Output: "coverage: 50.0% of statements\n"},
		{Action: testjson.ActionOutput, Package: "pkg2", Output: "coverage: 75.0% of statements\n"},
		{Action: testjson.ActionOutput, Package: "pkg3", Output: "coverage: 100% of statements\n"},
	}

	for _, event := range events {
		require.NoError(t, h.Event(event, nil))
	}

	assert.Equal(t, float32(50.0), h.coverage["pkg1"])
	assert.Equal(t, float32(75.0), h.coverage["pkg2"])
	assert.Equal(t, float32(100.0), h.coverage["pkg3"])
}

func TestShortPkg(t *testing.T) {
	t.Serial()
	tests := []struct {
		input    string
		expected string
	}{
		{"github.com/wow-look-at-my/go-toolchain/src/cmd", "cmd"},
		{"github.com/foo/bar", "bar"},
		{"standalone", "standalone"},
		{"a/b", "b"},
		{"", ""},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.expected, shortPkg(tc.input), "shortPkg(%q)", tc.input)
	}
}

func TestRealtimePassOutput(t *testing.T) {
	t.Serial()
	var buf bytes.Buffer
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: set.New[string](),
	}

	event := testjson.TestEvent{
		Action:  testjson.ActionPass,
		Package: "github.com/example/pkg",
		Test:    "TestFoo",
		Elapsed: 0.15,
	}
	require.NoError(t, h.Event(event, nil))

	output := buf.String()
	assert.Contains(t, output, "done.")
	assert.Contains(t, output, "pkg.TestFoo...")
	assert.Contains(t, output, "0.15s")
}

func TestRealtimePassOutputHiddenWhenFast(t *testing.T) {
	t.Serial()
	var buf bytes.Buffer
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: set.New[string](),
	}

	event := testjson.TestEvent{
		Action:  testjson.ActionPass,
		Package: "github.com/example/pkg",
		Test:    "TestFoo",
		Elapsed: 0.05,
	}
	require.NoError(t, h.Event(event, nil))

	assert.Empty(t, buf.String(), "passing tests under 0.1s should be hidden")
}

func TestRealtimeFailOutput(t *testing.T) {
	t.Serial()
	var buf bytes.Buffer
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: set.New[string](),
	}

	event := testjson.TestEvent{
		Action:  testjson.ActionFail,
		Package: "github.com/example/pkg",
		Test:    "TestBar",
		Elapsed: 1.23,
	}
	require.NoError(t, h.Event(event, nil))

	output := buf.String()
	assert.Contains(t, output, "failed!")
	assert.Contains(t, output, "pkg.TestBar...")
	assert.Contains(t, output, "1.23s")
}

func TestRealtimeTimeoutOutput(t *testing.T) {
	t.Serial()
	var buf bytes.Buffer
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: set.New[string](),
		timedOut:   set.New[string](),
	}

	// Simulate the timeout output event
	outputEvent := testjson.TestEvent{
		Action:  testjson.ActionOutput,
		Package: "github.com/example/pkg",
		Test:    "TestSlow",
		Output:  "panic: test timed out after 30s\n",
	}
	require.NoError(t, h.Event(outputEvent, nil))

	// Then simulate the fail event
	failEvent := testjson.TestEvent{
		Action:  testjson.ActionFail,
		Package: "github.com/example/pkg",
		Test:    "TestSlow",
		Elapsed: 30.0,
	}
	require.NoError(t, h.Event(failEvent, nil))

	output := buf.String()
	assert.Contains(t, output, "timed out!")
	assert.Contains(t, output, "pkg.TestSlow...")
	assert.NotContains(t, output, "failed!")
}

func TestRealtimeSkipOutput(t *testing.T) {
	t.Serial()
	var buf bytes.Buffer
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: set.New[string](),
	}

	event := testjson.TestEvent{
		Action:  testjson.ActionSkip,
		Package: "github.com/example/pkg",
		Test:    "TestSkipped",
		Elapsed: 0.5,
	}
	require.NoError(t, h.Event(event, nil))

	output := buf.String()
	assert.Contains(t, output, "skipped.")
	assert.Contains(t, output, "pkg.TestSkipped...")
}

func TestRealtimeSkipOutputHiddenWhenFast(t *testing.T) {
	t.Serial()
	var buf bytes.Buffer
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: set.New[string](),
	}

	event := testjson.TestEvent{
		Action:  testjson.ActionSkip,
		Package: "github.com/example/pkg",
		Test:    "TestSkipped",
		Elapsed: 0.0,
	}
	require.NoError(t, h.Event(event, nil))

	assert.Empty(t, buf.String(), "skipped tests under 0.1s should be hidden")
}

func TestRealtimeNoOutputInVerboseMode(t *testing.T) {
	t.Serial()
	var buf bytes.Buffer
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		verbose:    true,
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: set.New[string](),
	}

	event := testjson.TestEvent{
		Action:  testjson.ActionPass,
		Package: "github.com/example/pkg",
		Test:    "TestFoo",
		Elapsed: 0.05,
	}
	require.NoError(t, h.Event(event, nil))

	assert.Empty(t, buf.String(), "verbose mode should not print status lines")
}

func TestRealtimeNoOutputForPackageEvents(t *testing.T) {
	t.Serial()
	var buf bytes.Buffer
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: set.New[string](),
	}

	// Package-level pass (Test is empty)
	event := testjson.TestEvent{
		Action:  testjson.ActionPass,
		Package: "github.com/example/pkg",
		Elapsed: 2.5,
	}
	require.NoError(t, h.Event(event, nil))

	assert.Empty(t, buf.String(), "package-level events should not print status lines")
}

func TestFailureOutputWithStderr(t *testing.T) {
	t.Serial()
	h := &coverageHandler{
		coverage:    make(map[string]float32),
		testOutput:  make(map[string][]string),
		failedTest:  set.New[string](),
		stderrLines: []string{"build error: undefined reference", "linker failed"},
	}

	output := h.FailureOutput()
	assert.Contains(t, output, "build error: undefined reference\n")
	assert.Contains(t, output, "linker failed\n")
}

func TestFailureOutputWithFailedTests(t *testing.T) {
	t.Serial()
	h := &coverageHandler{
		coverage: make(map[string]float32),
		testOutput: map[string][]string{
			"pkg/TestFoo": {"    foo_test.go:10: expected 1, got 2\n"},
			"pkg/TestBar": {"    bar_test.go:5: nil pointer\n"},
		},
		failedTest: set.Of("pkg/TestFoo"),
	}

	output := h.FailureOutput()
	assert.Contains(t, output, "foo_test.go:10: expected 1, got 2")
	assert.NotContains(t, output, "bar_test.go:5: nil pointer")
}

func TestFailureOutputWithStderrAndFailedTests(t *testing.T) {
	t.Serial()
	h := &coverageHandler{
		coverage: make(map[string]float32),
		testOutput: map[string][]string{
			"pkg/TestFail": {"    assert failed\n"},
		},
		failedTest:  set.Of("pkg/TestFail"),
		stderrLines: []string{"compilation error"},
	}

	output := h.FailureOutput()
	// stderr leads
	assert.True(t, strings.Index(output, "compilation error") < strings.Index(output, "assert failed"))
}

func TestOnOutputCallbackInPass(t *testing.T) {
	t.Serial()
	var buf bytes.Buffer
	called := false
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: set.New[string](),
		onOutput:   func() { called = true },
	}

	event := testjson.TestEvent{
		Action:  testjson.ActionPass,
		Package: "github.com/example/pkg",
		Test:    "TestFoo",
		Elapsed: 0.15,
	}
	require.NoError(t, h.Event(event, nil))
	assert.True(t, called, "onOutput should be called on pass")
}

func TestOnOutputCallbackInSkip(t *testing.T) {
	t.Serial()
	var buf bytes.Buffer
	called := false
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: set.New[string](),
		onOutput:   func() { called = true },
	}

	event := testjson.TestEvent{
		Action:  testjson.ActionSkip,
		Package: "github.com/example/pkg",
		Test:    "TestSkipped",
		Elapsed: 0.5,
	}
	require.NoError(t, h.Event(event, nil))
	assert.True(t, called, "onOutput should be called on skip")
}

// TestFailureOutputKeepsBuildDiagnostics is the regression test for a build
// failure that printed as "FAIL <pkg> [build failed]" and nothing else. The
// compiler's diagnostics arrive as "build-output" events carrying ImportPath
// and an EMPTY Package, so they belonged to no per-package buffer and were
// dropped -- leaving a summary of an error nobody could see.
func TestFailureOutputKeepsBuildDiagnostics(t *testing.T) {
	t.Serial()
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		testOutput: make(map[string][]string),
		failedTest: set.New[string](),
		timedOut:   set.New[string](),
		out:        &bytes.Buffer{},
	}

	// What `go test -json` really emits for a package that will not compile.
	build := []testjson.TestEvent{
		{Action: testjson.ActionBuild, ImportPath: "example.com/pkg", Output: "# example.com/pkg\n"},
		{Action: testjson.ActionBuild, ImportPath: "example.com/pkg", Output: "./broken.go:7:2: undefined: nope\n"},
	}
	for _, e := range build {
		require.NoError(t, h.Event(e, nil))
	}
	// ...followed by the package summary, which is all that used to survive.
	require.NoError(t, h.Event(testjson.TestEvent{
		Action:  testjson.ActionOutput,
		Package: "example.com/pkg",
		Output:  "FAIL\texample.com/pkg [build failed]\n",
	}, nil))
	require.NoError(t, h.Event(testjson.TestEvent{
		Action:  testjson.ActionFail,
		Package: "example.com/pkg",
	}, nil))

	out := h.FailureOutput()
	assert.Contains(t, out, "undefined: nope", "the compiler diagnostic must survive")
	assert.Contains(t, out, "# example.com/pkg")
	assert.Contains(t, out, "[build failed]")

	// Order is the point: the error, then the summary of it.
	assert.Less(t, strings.Index(out, "undefined: nope"), strings.Index(out, "[build failed]"),
		"the summary must not precede the error it summarizes")
}

func TestFailureOutputOrdersBuildErrorsBeforeStderr(t *testing.T) {
	t.Serial()
	h := &coverageHandler{coverage: make(map[string]float32), out: &bytes.Buffer{}}
	require.NoError(t, h.Event(testjson.TestEvent{
		Action: testjson.ActionBuild, ImportPath: "example.com/pkg", Output: "./x.go:1:1: syntax error\n",
	}, nil))
	require.NoError(t, h.Err("go: some later complaint"))

	out := h.FailureOutput()
	assert.Less(t, strings.Index(out, "syntax error"), strings.Index(out, "later complaint"))
}
