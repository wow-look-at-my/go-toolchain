package trace

import (
	"context"
	"os"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/summary"
	"github.com/wow-look-at-my/go-containers/set"
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
	// Expected: 1 root + 2 workers + 4 steps = 7 spans
	require.Len(t, spans, 7, "expected 7 spans: 1 root + 2 workers + 4 steps")

	// Find root span.
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

	// Worker spans should be children of root, all named "build.worker", and
	// distinguished by the build.worker.id attribute.
	workerSpans := map[string]tracetest.SpanStub{}
	for _, s := range spans {
		if s.Parent.SpanID() == root.SpanContext.SpanID() {
			assert.Equal(t, "build.worker", s.Name)
			id := attrString(s.Attributes, "build.worker.id")
			require.NotEmpty(t, id, "build.worker.id missing on worker span")
			workerSpans[id] = s
		}
	}
	assert.Contains(t, workerSpans, "main")
	assert.Contains(t, workerSpans, "deps")

	// Step spans should be children of their worker span.
	mainWorker := workerSpans["main"]
	var mainSteps []string
	for _, s := range spans {
		if s.Parent.SpanID() == mainWorker.SpanContext.SpanID() {
			mainSteps = append(mainSteps, s.Name)
		}
	}
	assert.ElementsMatch(t, []string{"go mod tidy", "go vet", "tests"}, mainSteps)

	depsWorker := workerSpans["deps"]
	var depsSteps []string
	for _, s := range spans {
		if s.Parent.SpanID() == depsWorker.SpanContext.SpanID() {
			depsSteps = append(depsSteps, s.Name)
		}
	}
	assert.ElementsMatch(t, []string{"dep check"}, depsSteps)
}

// attrString returns the string value for the given attribute key, or "" if absent.
func attrString(attrs []attribute.KeyValue, key string) string {
	for _, a := range attrs {
		if string(a.Key) == key {
			return a.Value.AsString()
		}
	}
	return ""
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

	// Step spans are the leaves: anything that isn't the root or a worker.
	stepSpans := map[string]tracetest.SpanStub{}
	for _, s := range spans {
		if s.Name == "go-toolchain" || s.Name == "build.worker" {
			continue
		}
		stepSpans[s.Name] = s
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

func TestCompileLabelsBecomeStaticBuildCompile(t *testing.T) {
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
	// 1 root + 2 workers + 3 steps = 6
	require.Len(t, spans, 6)

	// Worker spans use the static name "build.worker" with a build.worker.id attribute.
	workerIDs := set.New[string]()
	for _, s := range spans {
		if s.Name == "build.worker" {
			workerIDs.Add(attrString(s.Attributes, "build.worker.id"))
		}
	}
	assert.True(t, workerIDs.Contains("worker-1"))
	assert.True(t, workerIDs.Contains("worker-2"))

	// All compile steps should share the static name "build.compile" and carry the
	// platform as attributes rather than baking it into the span name.
	type platform struct{ os, arch string }
	gotPlatforms := set.New[platform]()
	compileCount := 0
	for _, s := range spans {
		if s.Name == "build.compile" {
			compileCount++
			gotPlatforms.Add(platform{
				os:   attrString(s.Attributes, "build.target.os"),
				arch: attrString(s.Attributes, "build.target.arch"),
			})
		}
	}
	assert.Equal(t, 3, compileCount, "all three matrix entries should produce build.compile spans")
	assert.True(t, gotPlatforms.Contains(platform{"linux", "amd64"}))
	assert.True(t, gotPlatforms.Contains(platform{"linux", "arm64"}))
	assert.True(t, gotPlatforms.Contains(platform{"darwin", "amd64"}))
}

func TestSpansUseInternalKind(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := newTestProvider(exp)

	buildSpans(context.Background(), tp, testEntries())
	require.NoError(t, tp.ForceFlush(context.Background()))

	for _, s := range exp.GetSpans() {
		assert.Equal(t, trace.SpanKindInternal, s.SpanKind, "span %q should be INTERNAL", s.Name)
	}
}

func TestSuccessfulSpansSetOkStatus(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := newTestProvider(exp)

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	entries := []summary.TimelineEntry{
		{Label: "go vet", Thread: "main", Start: t0, End: t0.Add(time.Second)},
		{Label: "linux/amd64", Thread: "worker-1", Start: t0, End: t0.Add(time.Second)},
	}
	buildSpans(context.Background(), tp, entries)
	require.NoError(t, tp.ForceFlush(context.Background()))

	for _, s := range exp.GetSpans() {
		assert.Equal(t, codes.Ok, s.Status.Code, "span %q should have Ok status", s.Name)
	}
}

func TestNoLegacySuccessOrFailedAttributes(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := newTestProvider(exp)

	buildSpans(context.Background(), tp, testEntries())
	require.NoError(t, tp.ForceFlush(context.Background()))

	for _, s := range exp.GetSpans() {
		for _, a := range s.Attributes {
			key := string(a.Key)
			assert.NotEqual(t, "build.success", key, "build.success attribute should not be set; use span status")
			assert.NotEqual(t, "step.failed", key, "step.failed attribute should not be set; use span status")
		}
	}
}

func TestGitHubResourceAttributes(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "")
	t.Setenv("GITHUB_SHA", "abc123")
	t.Setenv("GITHUB_REPOSITORY", "wow-look-at-my/go-toolchain")
	t.Setenv("GITHUB_REF", "refs/heads/main")
	t.Setenv("GITHUB_RUN_ID", "12345")

	res, err := buildProviderResource(context.Background())
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

// TestRootIDGenerator verifies that the first NewIDs call returns the
// pre-determined traceparent IDs — the invariant that makes cacheprog
// spans nest under the go-toolchain root span instead of appearing as
// a separate trace. Subsequent calls fall back to random IDs.
func TestRootIDGenerator(t *testing.T) {
	tid, err := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	require.NoError(t, err)
	sid, err := trace.SpanIDFromHex("1112131415161718")
	require.NoError(t, err)

	g := newRootIDGenerator(tid, sid)

	gotTID, gotSID := g.NewIDs(context.Background())
	assert.Equal(t, tid, gotTID, "first NewIDs must return the seeded traceID")
	assert.Equal(t, sid, gotSID, "first NewIDs must return the seeded spanID")

	_, nextSID := g.NewIDs(context.Background())
	assert.NotEqual(t, sid, nextSID, "second NewIDs must generate a fresh spanID")

	childSID := g.NewSpanID(context.Background(), tid)
	assert.NotEqual(t, sid, childSID, "NewSpanID must always be random")
}
