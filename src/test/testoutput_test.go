package test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gotest.tools/gotestsum/testjson"
)

func TestRealtimePassOutput(t *testing.T) {
	var buf bytes.Buffer
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: make(map[string]bool),
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
	var buf bytes.Buffer
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: make(map[string]bool),
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
	var buf bytes.Buffer
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: make(map[string]bool),
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
	var buf bytes.Buffer
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: make(map[string]bool),
		timedOut:   make(map[string]bool),
	}

	// First, simulate timeout output event
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
	var buf bytes.Buffer
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: make(map[string]bool),
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
	var buf bytes.Buffer
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: make(map[string]bool),
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
	var buf bytes.Buffer
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		verbose:    true,
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: make(map[string]bool),
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
	var buf bytes.Buffer
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: make(map[string]bool),
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
	h := &coverageHandler{
		coverage:    make(map[string]float32),
		testOutput:  make(map[string][]string),
		failedTest:  make(map[string]bool),
		stderrLines: []string{"build error: undefined reference", "linker failed"},
	}

	output := h.FailureOutput()
	assert.Contains(t, output, "build error: undefined reference\n")
	assert.Contains(t, output, "linker failed\n")
}

func TestFailureOutputWithFailedTests(t *testing.T) {
	h := &coverageHandler{
		coverage: make(map[string]float32),
		testOutput: map[string][]string{
			"pkg/TestFoo": {"    foo_test.go:10: expected 1, got 2\n"},
			"pkg/TestBar": {"    bar_test.go:5: nil pointer\n"},
		},
		failedTest: map[string]bool{
			"pkg/TestFoo": true,
		},
	}

	output := h.FailureOutput()
	assert.Contains(t, output, "foo_test.go:10: expected 1, got 2")
	assert.NotContains(t, output, "bar_test.go:5: nil pointer")
}

func TestFailureOutputWithStderrAndFailedTests(t *testing.T) {
	h := &coverageHandler{
		coverage: make(map[string]float32),
		testOutput: map[string][]string{
			"pkg/TestFail": {"    assert failed\n"},
		},
		failedTest: map[string]bool{
			"pkg/TestFail": true,
		},
		stderrLines: []string{"compilation error"},
	}

	output := h.FailureOutput()
	// stderr comes first
	assert.True(t, strings.Index(output, "compilation error") < strings.Index(output, "assert failed"))
}

func TestOnOutputCallbackInPass(t *testing.T) {
	var buf bytes.Buffer
	called := false
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: make(map[string]bool),
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
	var buf bytes.Buffer
	called := false
	h := &coverageHandler{
		coverage:   make(map[string]float32),
		out:        &buf,
		testOutput: make(map[string][]string),
		failedTest: make(map[string]bool),
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
