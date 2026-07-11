package trace

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/wow-look-at-my/go-toolchain/src/summary"
)

// Export converts timeline entries to OTel spans and emits them through
// the shared tracer provider (see provider.go). It is a no-op if
// OTEL_EXPORTER_OTLP_ENDPOINT is unset or if entries is empty.
//
// The shared provider owns the batcher and exporter for the whole
// build, so this function doesn't build or shut down its own — the
// build entrypoint calls Shutdown once, after Export, which flushes
// every emitter's queued spans in a single OTLP round-trip.
func Export(ctx context.Context, entries []summary.TimelineEntry) error {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" || len(entries) == 0 {
		return nil
	}

	tp, err := Provider(ctx)
	if err != nil {
		return err
	}
	if tp == nil {
		return nil
	}

	fmt.Fprintf(os.Stderr, "⇒ Exporting %d timeline entries to %s\n", len(entries), endpoint)
	buildSpans(ctx, tp, entries)
	return nil
}

// buildSpans creates the three-level span hierarchy: root → worker → step.
func buildSpans(ctx context.Context, tp *sdktrace.TracerProvider, entries []summary.TimelineEntry) {
	tracer := tp.Tracer("go-toolchain")

	// Compute overall time bounds.
	minStart, maxEnd := entries[0].Start, entries[0].End
	allSuccess := true
	for _, e := range entries {
		if e.Start.Before(minStart) {
			minStart = e.Start
		}
		if e.End.After(maxEnd) {
			maxEnd = e.End
		}
		if e.Failed {
			allSuccess = false
		}
	}

	// Group entries by thread, preserving order.
	threadOrder, threadEntries := groupByThread(entries)

	// Root span.
	rootCtx, rootSpan := tracer.Start(ctx, "go-toolchain",
		trace.WithTimestamp(minStart),
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.Int("build.workers", len(threadOrder)),
			attribute.Int("build.steps", len(entries)),
		),
	)
	if allSuccess {
		rootSpan.SetStatus(codes.Ok, "")
	} else {
		rootSpan.SetStatus(codes.Error, "build had failures")
	}

	// Worker and step spans.
	for _, thread := range threadOrder {
		tes := threadEntries[thread]
		tStart, tEnd := threadBounds(tes)

		workerSuccess := true
		for _, e := range tes {
			if e.Failed {
				workerSuccess = false
				break
			}
		}

		threadCtx, workerSpan := tracer.Start(rootCtx, "build.worker",
			trace.WithTimestamp(tStart),
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(attribute.String("build.worker.id", thread)),
		)
		if workerSuccess {
			workerSpan.SetStatus(codes.Ok, "")
		} else {
			workerSpan.SetStatus(codes.Error, "worker had failures")
		}

		for _, e := range tes {
			name, attrs := stepSpanInfo(e)
			_, stepSpan := tracer.Start(threadCtx, name,
				trace.WithTimestamp(e.Start),
				trace.WithSpanKind(trace.SpanKindInternal),
				trace.WithAttributes(attrs...),
			)
			if e.Failed {
				stepSpan.SetStatus(codes.Error, "step failed")
			} else {
				stepSpan.SetStatus(codes.Ok, "")
			}
			stepSpan.End(trace.WithTimestamp(e.End))
		}

		workerSpan.End(trace.WithTimestamp(tEnd))
	}

	rootSpan.End(trace.WithTimestamp(maxEnd))
}

// stepSpanInfo returns a static span name and the attributes describing the step.
// Cross-compile labels formatted as "os/arch" (e.g. "linux/amd64") collapse into a
// single "build.compile" span with the platform recorded as attributes; all other
// labels are preserved as-is and assumed to already be static.
func stepSpanInfo(e summary.TimelineEntry) (string, []attribute.KeyValue) {
	attrs := []attribute.KeyValue{
		attribute.String("build.worker.id", e.Thread),
	}
	if goos, goarch, ok := strings.Cut(e.Label, "/"); ok && goos != "" && goarch != "" &&
		!strings.ContainsAny(goos, " /") && !strings.ContainsAny(goarch, " /") {
		attrs = append(attrs,
			attribute.String("build.target.os", goos),
			attribute.String("build.target.arch", goarch),
		)
		return "build.compile", attrs
	}
	return e.Label, attrs
}

// groupByThread groups entries by thread, preserving first-seen order.
func groupByThread(entries []summary.TimelineEntry) ([]string, map[string][]summary.TimelineEntry) {
	order := []string{}
	grouped := make(map[string][]summary.TimelineEntry)
	for _, e := range entries {
		if _, exists := grouped[e.Thread]; !exists {
			order = append(order, e.Thread)
		}
		grouped[e.Thread] = append(grouped[e.Thread], e)
	}
	// Sort entries within each thread by start time.
	for _, tes := range grouped {
		sort.Slice(tes, func(i, j int) bool {
			return tes[i].Start.Before(tes[j].Start)
		})
	}
	return order, grouped
}

// threadBounds returns the min start and max end for a slice of entries.
func threadBounds(entries []summary.TimelineEntry) (time.Time, time.Time) {
	minStart, maxEnd := entries[0].Start, entries[0].End
	for _, e := range entries[1:] {
		if e.Start.Before(minStart) {
			minStart = e.Start
		}
		if e.End.After(maxEnd) {
			maxEnd = e.End
		}
	}
	return minStart, maxEnd
}
