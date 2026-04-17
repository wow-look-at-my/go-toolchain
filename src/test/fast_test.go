package test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
	"gotest.tools/gotestsum/testjson"
)

func newTestHandler(buf *bytes.Buffer) *coverageHandler {
	return &coverageHandler{
		coverage:   make(map[string]float32),
		out:        buf,
		testOutput: make(map[string][]string),
		failedTest: make(map[string]bool),
		timedOut:   make(map[string]bool),
	}
}

func fastPassEvent(pkg, test string, elapsed float64) testjson.TestEvent {
	return testjson.TestEvent{
		Action: testjson.ActionPass, Package: pkg, Test: test, Elapsed: elapsed,
	}
}

func fastSkipEvent(pkg, test string, elapsed float64) testjson.TestEvent {
	return testjson.TestEvent{
		Action: testjson.ActionSkip, Package: pkg, Test: test, Elapsed: elapsed,
	}
}

func TestFastTestSummaryOnFlush(t *testing.T) {
	var buf bytes.Buffer
	h := newTestHandler(&buf)

	for i := 0; i < 5; i++ {
		require.NoError(t, h.Event(fastPassEvent("example.com/pkg", fmt.Sprintf("TestFast%d", i), 0.01), nil))
	}
	assert.Empty(t, buf.String(), "no output before flush")

	h.flushFast()
	assert.Contains(t, buf.String(), "[5 fast tests]")
}

func TestFastTestSummaryFlushedBeforeSlowTest(t *testing.T) {
	var buf bytes.Buffer
	h := newTestHandler(&buf)

	for i := 0; i < 3; i++ {
		require.NoError(t, h.Event(fastPassEvent("example.com/pkg", fmt.Sprintf("TestFast%d", i), 0.02), nil))
	}
	require.NoError(t, h.Event(testjson.TestEvent{
		Action: testjson.ActionPass, Package: "example.com/pkg", Test: "TestSlow", Elapsed: 0.5,
	}, nil))

	output := buf.String()
	fastIdx := strings.Index(output, "[3 fast tests]")
	slowIdx := strings.Index(output, "done.")
	assert.Greater(t, fastIdx, -1)
	assert.Greater(t, slowIdx, -1)
	assert.Less(t, fastIdx, slowIdx, "fast summary should appear before slow test")
}

func TestFastTestSummaryFlushedBeforeFailure(t *testing.T) {
	var buf bytes.Buffer
	h := newTestHandler(&buf)

	for i := 0; i < 2; i++ {
		require.NoError(t, h.Event(fastPassEvent("example.com/pkg", fmt.Sprintf("TestFast%d", i), 0.01), nil))
	}
	require.NoError(t, h.Event(testjson.TestEvent{
		Action: testjson.ActionFail, Package: "example.com/pkg", Test: "TestBroken", Elapsed: 0.3,
	}, nil))

	output := buf.String()
	fastIdx := strings.Index(output, "[2 fast tests]")
	failIdx := strings.Index(output, "failed!")
	assert.Greater(t, fastIdx, -1)
	assert.Greater(t, failIdx, -1)
	assert.Less(t, fastIdx, failIdx, "fast summary should appear before failure")
}

func TestFastTestSummaryWithMixedFastSkips(t *testing.T) {
	var buf bytes.Buffer
	h := newTestHandler(&buf)

	require.NoError(t, h.Event(fastPassEvent("example.com/pkg", "TestA", 0.01), nil))
	require.NoError(t, h.Event(fastSkipEvent("example.com/pkg", "TestB", 0.02), nil))
	require.NoError(t, h.Event(fastPassEvent("example.com/pkg", "TestC", 0.03), nil))

	h.flushFast()
	assert.Contains(t, buf.String(), "[3 fast tests]")
}

func TestFastTestSummaryMultipleBatches(t *testing.T) {
	var buf bytes.Buffer
	h := newTestHandler(&buf)

	for i := 0; i < 3; i++ {
		require.NoError(t, h.Event(fastPassEvent("example.com/pkg", fmt.Sprintf("TestBatch1_%d", i), 0.01), nil))
	}
	require.NoError(t, h.Event(testjson.TestEvent{
		Action: testjson.ActionPass, Package: "example.com/pkg", Test: "TestSlow1", Elapsed: 0.5,
	}, nil))
	for i := 0; i < 2; i++ {
		require.NoError(t, h.Event(fastPassEvent("example.com/pkg", fmt.Sprintf("TestBatch2_%d", i), 0.02), nil))
	}
	h.flushFast()

	output := buf.String()
	assert.Contains(t, output, "[3 fast tests]")
	assert.Contains(t, output, "[2 fast tests]")
}

func TestFastTestSummarySkippedInVerbose(t *testing.T) {
	var buf bytes.Buffer
	h := newTestHandler(&buf)
	h.verbose = true

	for i := 0; i < 5; i++ {
		require.NoError(t, h.Event(fastPassEvent("example.com/pkg", fmt.Sprintf("TestFast%d", i), 0.01), nil))
	}
	h.flushFast()
	assert.Empty(t, buf.String(), "verbose mode should not produce fast test summary")
}

func TestFastTestSummarySingular(t *testing.T) {
	var buf bytes.Buffer
	h := newTestHandler(&buf)

	require.NoError(t, h.Event(fastPassEvent("example.com/pkg", "TestOnly", 0.05), nil))
	h.flushFast()

	output := buf.String()
	assert.Contains(t, output, "[1 fast test]")
	assert.NotContains(t, output, "fast tests]")
}

func TestFastTestSummaryCallsOnOutput(t *testing.T) {
	var buf bytes.Buffer
	called := false
	h := newTestHandler(&buf)
	h.onOutput = func() { called = true }

	require.NoError(t, h.Event(fastPassEvent("example.com/pkg", "TestFast", 0.01), nil))
	h.flushFast()

	assert.True(t, called, "onOutput should be called from flushFast")
}

func TestFastTestNoSummaryWhenNoFastTests(t *testing.T) {
	var buf bytes.Buffer
	h := newTestHandler(&buf)

	require.NoError(t, h.Event(testjson.TestEvent{
		Action: testjson.ActionPass, Package: "example.com/pkg", Test: "TestSlow", Elapsed: 0.5,
	}, nil))
	h.flushFast()

	output := buf.String()
	assert.NotContains(t, output, "fast test")
	assert.Contains(t, output, "done.")
}
