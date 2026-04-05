package trace

import (
	"context"
	"os"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/wow-look-at-my/go-toolchain/src/summary"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func testEntries() []summary.TimelineEntry {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return []summary.TimelineEntry{
		{Label: "go mod tidy", Thread: "main", Start: t0, End: t0.Add(2 * time.Second)},
		{Label: "go vet", Thread: "main", Start: t0.Add(2 * time.Second), End: t0.Add(5 * time.Second)},
		{Label: "tests", Thread: "main", Start: t0.Add(5 * time.Second), End: t0.Add(15 * time.Second), Failed: true},
		{Label: "dep check", Thread: "deps", Start: t0.Add(1 * time.Second), End: t0.Add(8 * time.Second)},
	}
}

func newTestProvider(exp *tracetest.InMemoryExporter) *sdktrace.TracerProvider {
	return sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exp),
	)
}

func TestExportNoOp(t *testing.T) {
	os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	err := Export(context.Background(), testEntries())
	assert.NoError(t, err)
}

func TestExportNoOpEmpty(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")
	err := Export(context.Background(), nil)
	assert.NoError(t, err)
}

func TestBuildSpanHierarchy(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := newTestProvider(exp)

	entries := testEntries()
	buildSpans(context.Background(), tp, entries)
	require.NoError(t, tp.ForceFlush(context.Background()))

	spans := exp.GetSpans()
	// Expected: 1 root + 2 threads + 4 steps = 7 spans
	require.Len(t, spans, 7, "expected 7 spans: 1 root + 2 threads + 4 steps")

	// Find root span (no parent).
	var root tracetest.SpanStub
	var rootFound bool
	for _, s := range spans {
		if s.Name == "go-toolchain" {
			root = s
			rootFound = true
			break
		}
	}
	require.True(t, rootFound, "root span not found")

	// Thread spans should be children of root.
	threadSpans := map[string]tracetest.SpanStub{}
	for _, s := range spans {
		if s.Parent.SpanID() == root.SpanContext.SpanID() {
			threadSpans[s.Name] = s
		}
	}
	assert.Contains(t, threadSpans, "thread:main")
	assert.Contains(t, threadSpans, "thread:deps")

	// Step spans should be children of their thread span.
	mainThread := threadSpans["thread:main"]
	var mainSteps []string
	for _, s := range spans {
		if s.Parent.SpanID() == mainThread.SpanContext.SpanID() {
			mainSteps = append(mainSteps, s.Name)
		}
	}
	assert.ElementsMatch(t, []string{"go mod tidy", "go vet", "tests"}, mainSteps)

	depsThread := threadSpans["thread:deps"]
	var depsSteps []string
	for _, s := range spans {
		if s.Parent.SpanID() == depsThread.SpanContext.SpanID() {
			depsSteps = append(depsSteps, s.Name)
		}
	}
	assert.ElementsMatch(t, []string{"dep check"}, depsSteps)
}

func TestFailedStepSetsErrorStatus(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := newTestProvider(exp)

	buildSpans(context.Background(), tp, testEntries())
	require.NoError(t, tp.ForceFlush(context.Background()))

	spans := exp.GetSpans()

	// Find the "tests" span which has Failed=true.
	var found bool
	for _, s := range spans {
		if s.Name == "tests" {
			assert.Equal(t, codes.Error, s.Status.Code)
			assert.Equal(t, "step failed", s.Status.Description)
			found = true
			break
		}
	}
	require.True(t, found, "failed step span not found")

	// Root span should also have error status since build has failures.
	for _, s := range spans {
		if s.Name == "go-toolchain" {
			assert.Equal(t, codes.Error, s.Status.Code)
			break
		}
	}
}

func TestSpanTimestampsMatchEntries(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := newTestProvider(exp)

	entries := testEntries()
	buildSpans(context.Background(), tp, entries)
	require.NoError(t, tp.ForceFlush(context.Background()))

	spans := exp.GetSpans()

	// Build a map of step spans by name.
	stepSpans := map[string]tracetest.SpanStub{}
	for _, s := range spans {
		// Step spans are the ones that aren't root or thread spans.
		if s.Name != "go-toolchain" && len(s.Name) < 8 || (len(s.Name) >= 8 && s.Name[:7] != "thread:") {
			stepSpans[s.Name] = s
		}
	}

	for _, e := range entries {
		s, ok := stepSpans[e.Label]
		if !ok {
			continue
		}
		assert.Equal(t, e.Start, s.StartTime, "start time mismatch for %s", e.Label)
		assert.Equal(t, e.End, s.EndTime, "end time mismatch for %s", e.Label)
	}
}

func TestThreadGrouping(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := newTestProvider(exp)

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	entries := []summary.TimelineEntry{
		{Label: "linux/amd64", Thread: "worker-1", Start: t0, End: t0.Add(10 * time.Second)},
		{Label: "linux/arm64", Thread: "worker-2", Start: t0, End: t0.Add(12 * time.Second)},
		{Label: "darwin/amd64", Thread: "worker-1", Start: t0.Add(10 * time.Second), End: t0.Add(18 * time.Second)},
	}

	buildSpans(context.Background(), tp, entries)
	require.NoError(t, tp.ForceFlush(context.Background()))

	spans := exp.GetSpans()
	// 1 root + 2 threads + 3 steps = 6
	require.Len(t, spans, 6)

	threadNames := map[string]bool{}
	for _, s := range spans {
		if len(s.Name) > 7 && s.Name[:7] == "thread:" {
			threadNames[s.Name] = true
		}
	}
	assert.True(t, threadNames["thread:worker-1"])
	assert.True(t, threadNames["thread:worker-2"])
}

func TestGitHubResourceAttributes(t *testing.T) {
	t.Setenv("GITHUB_SHA", "abc123")
	t.Setenv("GITHUB_REPOSITORY", "wow-look-at-my/go-toolchain")
	t.Setenv("GITHUB_REF", "refs/heads/main")
	t.Setenv("GITHUB_RUN_ID", "12345")

	res, err := buildResource(context.Background())
	require.NoError(t, err)

	attrs := res.Attributes()
	attrMap := map[string]string{}
	for _, a := range attrs {
		attrMap[string(a.Key)] = a.Value.AsString()
	}

	assert.Equal(t, "go-toolchain", attrMap["service.name"])
	assert.Equal(t, "abc123", attrMap["github.sha"])
	assert.Equal(t, "wow-look-at-my/go-toolchain", attrMap["github.repository"])
	assert.Equal(t, "refs/heads/main", attrMap["github.ref"])
	assert.Equal(t, "12345", attrMap["github.run_id"])
}
