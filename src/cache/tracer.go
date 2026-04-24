package cache

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// noopTracer is returned by cacheTracer.Start when tracing is disabled,
// so callers can use the standard `ctx, span := t.Start(...); defer span.End()`
// idiom without nil guards. The trace.Span interface is sealed, so we
// must use the official otel noop package rather than stubbing it.
var noopTracer = noop.NewTracerProvider().Tracer("noop")

// cacheTracer owns the OTel tracer provider used by cache-mode components
// (the WebBackend's HTTP operations and the httpErrLogger). It is created
// once per WebBackend so every cache-mode span shares the same exporter
// batcher and the same parent trace context propagated from go-toolchain
// via OTEL_TRACEPARENT.
//
// All methods are nil-safe: if OTEL_EXPORTER_OTLP_ENDPOINT is unset or
// exporter init fails, newCacheTracer returns nil and Start becomes a
// no-op (returns a fresh context and a noop span).
type cacheTracer struct {
	tp            *sdktrace.TracerProvider
	tracer        trace.Tracer
	parentSpanCtx trace.SpanContext
}

// newCacheTracer initializes an OTLP HTTP tracer provider if
// OTEL_EXPORTER_OTLP_ENDPOINT is set. Returns nil (tracing disabled) when
// the env var is unset or when exporter init fails — errors are reported
// to w but never propagated, so cache mode stays functional even if the
// telemetry backend is down.
func newCacheTracer(w io.Writer) *cacheTracer {
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tp, err := newCacheTracerProvider(ctx)
	if err != nil {
		fmt.Fprintf(w, "cacheprog: otel init failed: %v\n", err)
		return nil
	}
	return &cacheTracer{
		tp:            tp,
		tracer:        tp.Tracer("go-toolchain/cacheprog"),
		parentSpanCtx: extractParentSpanContext(),
	}
}

// Start begins a span rooted at the propagated parent trace context. The
// returned context should be passed to any downstream spans so they nest
// properly. Nil-safe: returns a fresh background context and a noop span
// when tracing is disabled, letting callers use the standard
// `ctx, span := t.Start(...); defer span.End()` idiom without guards.
func (t *cacheTracer) Start(name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	if t == nil || t.tracer == nil {
		return noopTracer.Start(context.Background(), name)
	}
	ctx := context.Background()
	if t.parentSpanCtx.IsValid() {
		ctx = trace.ContextWithSpanContext(ctx, t.parentSpanCtx)
	}
	return t.tracer.Start(ctx, name, trace.WithAttributes(attrs...))
}

// Shutdown force-flushes and shuts down the tracer provider. Nil-safe and
// idempotent — safe to call from defers even if tracing was never enabled.
func (t *cacheTracer) Shutdown() {
	if t == nil || t.tp == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = t.tp.ForceFlush(ctx)
	_ = t.tp.Shutdown(ctx)
}

// Enabled reports whether OTel tracing is configured. Callers that need
// to build expensive attribute sets can guard the work with this check.
func (t *cacheTracer) Enabled() bool {
	return t != nil && t.tracer != nil
}

// markSpanMiss tags a cache span as a miss with a structured reason. The
// `cacheprog.miss_reason` attribute is the primary filter for miss
// diagnostics — keep reason strings stable across releases.
func markSpanMiss(span trace.Span, reason string) {
	span.SetAttributes(
		attribute.Bool("cacheprog.miss", true),
		attribute.String("cacheprog.miss_reason", reason),
	)
}

// markSpanErr records err on the span and marks it failed at the named
// stage. err must be non-nil — for error statuses without an underlying
// Go error (HTTP status codes, etc.), call span.SetStatus directly.
func markSpanErr(span trace.Span, stage string, err error) {
	span.RecordError(err)
	span.SetStatus(codes.Error, stage)
}

func newCacheTracerProvider(ctx context.Context) (*sdktrace.TracerProvider, error) {
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	res, err := buildCacheResource(ctx)
	if err != nil {
		return nil, err
	}
	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	), nil
}

func buildCacheResource(ctx context.Context) (*resource.Resource, error) {
	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "go-toolchain"
	}
	attrs := []attribute.KeyValue{
		semconv.ServiceName(serviceName),
	}
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

func extractParentSpanContext() trace.SpanContext {
	traceparent := os.Getenv("OTEL_TRACEPARENT")
	if traceparent == "" {
		return trace.SpanContext{}
	}
	parts := strings.Split(traceparent, "-")
	if len(parts) != 4 {
		return trace.SpanContext{}
	}
	traceID, err := trace.TraceIDFromHex(parts[1])
	if err != nil {
		return trace.SpanContext{}
	}
	spanID, err := trace.SpanIDFromHex(parts[2])
	if err != nil {
		return trace.SpanContext{}
	}
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
		Remote:  true,
	})
}

