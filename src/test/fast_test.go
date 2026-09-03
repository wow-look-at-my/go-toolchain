package test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-containers/set"
	"gotest.tools/gotestsum/testjson"
)

func newTestHandler(buf *bytes.Buffer) *coverageHandler {
	return &coverageHandler{
		coverage:   make(map[string]float32),
		out:        buf,
		testOutput: make(map[string][]string),
		failedTest: set.New[string](),
		timedOut:   set.New[string](),
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

func TestFastTestSummaryAtEndOfRun(t *testing.T) {
	var buf bytes.Buffer
	h := newTestHandler(&buf)

	for i := 0; i < 5; i++ {
		require.NoError(t, h.Event(fastPassEvent("example.com/pkg", fmt.Sprintf("TestFast%d", i), 0.01), nil))
	}
	assert.Empty(t, buf.String(), "no output before end-of-run summary")

	h.printFastSummary()
	assert.Contains(t, buf.String(), "[5 fast tests]")
}

func TestFastTestSummaryDeferredUntilEnd(t *testing.T) {
	var buf bytes.Buffer
	h := newTestHandler(&buf)

	for i := 0; i < 3; i++ {
		require.NoError(t, h.Event(fastPassEvent("example.com/pkg", fmt.Sprintf("TestFast%d", i), 0.02), nil))
	}
	require.NoError(t, h.Event(testjson.TestEvent{
		Action: testjson.ActionPass, Package: "example.com/pkg", Test: "TestSlow", Elapsed: 0.5,
	}, nil))

	assert.NotContains(t, buf.String(), "fast test", "fast summary must not appear mid-run")
	assert.Contains(t, buf.String(), "done.", "slow test should have printed")

	h.printFastSummary()
	output := buf.String()
	fastIdx := strings.Index(output, "[3 fast tests]")
	slowIdx := strings.Index(output, "done.")
	assert.Greater(t, fastIdx, -1)
	assert.Greater(t, slowIdx, -1)
	assert.Less(t, slowIdx, fastIdx, "fast summary should appear after slow test")
}

func TestFastTestSummaryDeferredAfterFailure(t *testing.T) {
	var buf bytes.Buffer
	h := newTestHandler(&buf)

	for i := 0; i < 2; i++ {
		require.NoError(t, h.Event(fastPassEvent("example.com/pkg", fmt.Sprintf("TestFast%d", i), 0.01), nil))
	}
	require.NoError(t, h.Event(testjson.TestEvent{
		Action: testjson.ActionFail, Package: "example.com/pkg", Test: "TestBroken", Elapsed: 0.3,
	}, nil))

	assert.NotContains(t, buf.String(), "fast test", "fast summary must not appear mid-run")
	assert.Contains(t, buf.String(), "failed!", "failure should have printed")

	h.printFastSummary()
	output := buf.String()
	fastIdx := strings.Index(output, "[2 fast tests]")
	failIdx := strings.Index(output, "failed!")
	assert.Greater(t, fastIdx, -1)
	assert.Greater(t, failIdx, -1)
	assert.Less(t, failIdx, fastIdx, "fast summary should appear after failure")
}

func TestFastTestSummaryWithMixedFastSkips(t *testing.T) {
	var buf bytes.Buffer
	h := newTestHandler(&buf)

	require.NoError(t, h.Event(fastPassEvent("example.com/pkg", "TestA", 0.01), nil))
	require.NoError(t, h.Event(fastSkipEvent("example.com/pkg", "TestB", 0.02), nil))
	require.NoError(t, h.Event(fastPassEvent("example.com/pkg", "TestC", 0.03), nil))

	h.printFastSummary()
	assert.Contains(t, buf.String(), "[3 fast tests]")
}

func TestFastTestSummaryAccumulatesAcrossSlowTests(t *testing.T) {
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
	h.printFastSummary()

	output := buf.String()
	assert.Contains(t, output, "[5 fast tests]", "all fast tests should accumulate into a single summary")
	assert.NotContains(t, output, "[3 fast tests]", "no per-batch summary")
	assert.NotContains(t, output, "[2 fast tests]", "no per-batch summary")
}

func TestFastTestSummarySkippedInVerbose(t *testing.T) {
	t.Serial()
	var buf bytes.Buffer
	h := newTestHandler(&buf)
	h.verbose = true

	for i := 0; i < 5; i++ {
		require.NoError(t, h.Event(fastPassEvent("example.com/pkg", fmt.Sprintf("TestFast%d", i), 0.01), nil))
	}
	h.printFastSummary()
	assert.Empty(t, buf.String(), "verbose mode should not produce fast test summary")
}

func TestFastTestSummarySingular(t *testing.T) {
	var buf bytes.Buffer
	h := newTestHandler(&buf)

	require.NoError(t, h.Event(fastPassEvent("example.com/pkg", "TestOnly", 0.05), nil))
	h.printFastSummary()

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
	h.printFastSummary()

	assert.True(t, called, "onOutput should be called from printFastSummary")
}

func TestFastTestNoSummaryWhenNoFastTests(t *testing.T) {
	var buf bytes.Buffer
	h := newTestHandler(&buf)

	require.NoError(t, h.Event(testjson.TestEvent{
		Action: testjson.ActionPass, Package: "example.com/pkg", Test: "TestSlow", Elapsed: 0.5,
	}, nil))
	h.printFastSummary()

	output := buf.String()
	assert.NotContains(t, output, "fast test")
	assert.Contains(t, output, "done.")
}
