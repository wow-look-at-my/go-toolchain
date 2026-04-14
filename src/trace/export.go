package trace

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/wow-look-at-my/go-toolchain/src/summary"
)

// Export converts timeline entries to OTel spans and exports them via OTLP/HTTP.
// It is a no-op if OTEL_EXPORTER_OTLP_ENDPOINT is unset or if entries is empty.
func Export(ctx context.Context, entries []summary.TimelineEntry) error {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" || len(entries) == 0 {
		return nil
	}
	fmt.Fprintf(os.Stderr, "==> Exporting %d timeline entries to %s\n", len(entries), endpoint)

	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return err
	}

	res, err := buildResource(ctx)
	if err != nil {
		return err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	buildSpans(ctx, tp, entries)

	if err := tp.ForceFlush(ctx); err != nil {
		return err
	}
	return tp.Shutdown(ctx)
}

// buildSpans creates the three-level span hierarchy: root → thread → step.
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
		trace.WithAttributes(
			attribute.Int("build.threads", len(threadOrder)),
			attribute.Int("build.steps", len(entries)),
			attribute.Bool("build.success", allSuccess),
		),
	)
	if !allSuccess {
		rootSpan.SetStatus(codes.Error, "build had failures")
	}

	// Thread and step spans.
	for _, thread := range threadOrder {
		tes := threadEntries[thread]
		tStart, tEnd := threadBounds(tes)

		threadCtx, threadSpan := tracer.Start(rootCtx, "thread:"+thread,
			trace.WithTimestamp(tStart),
			trace.WithAttributes(attribute.String("thread.name", thread)),
		)

		for _, e := range tes {
			_, stepSpan := tracer.Start(threadCtx, e.Label,
				trace.WithTimestamp(e.Start),
				trace.WithAttributes(
					attribute.String("step.thread", e.Thread),
					attribute.Bool("step.failed", e.Failed),
				),
			)
			if e.Failed {
				stepSpan.SetStatus(codes.Error, "step failed")
			}
			stepSpan.End(trace.WithTimestamp(e.End))
		}

		threadSpan.End(trace.WithTimestamp(tEnd))
	}

	rootSpan.End(trace.WithTimestamp(maxEnd))
}

// buildResource creates the OTel resource with service name and GitHub CI attributes.
func buildResource(ctx context.Context) (*resource.Resource, error) {
	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "go-toolchain"
	}

	attrs := []attribute.KeyValue{
		semconv.ServiceName(serviceName),
	}

	// Add GitHub CI attributes when available.
	for _, kv := range []struct{ envVar, attrKey string }{
		{"GITHUB_SHA", "github.sha"},
		{"GITHUB_REPOSITORY", "github.repository"},
		{"GITHUB_REF", "github.ref"},
		{"GITHUB_RUN_ID", "github.run_id"},
		{"GITHUB_RUN_ATTEMPT", "github.run_attempt"},
	} {
		if v := os.Getenv(kv.envVar); v != "" {
			attrs = append(attrs, attribute.String(kv.attrKey, v))
		}
	}

	return resource.New(ctx, resource.WithAttributes(attrs...))
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
