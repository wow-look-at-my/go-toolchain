package cache

import (
	"context"
	"io"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	gotrace "github.com/wow-look-at-my/go-toolchain/src/trace"
)

// noopTracer is returned by cacheTracer.Start when tracing is disabled,
// so callers can use the standard `ctx, span := t.Start(...); defer span.End()`
// idiom without nil guards. The trace.Span interface is sealed, so we
// must use the official otel noop package rather than stubbing it.
var noopTracer = noop.NewTracerProvider().Tracer("noop")

// cacheTracer is a thin façade over the process-wide shared tracer
// provider (src/trace). It exists only to:
//   - hold the parent SpanContext parsed from OTEL_TRACEPARENT so the
//     cache package can root its spans under the go-toolchain run;
//   - route every Start through the shared provider, keeping cache ops
//     and timeline spans in a single batcher and a single OTLP stream.
//
// All methods are nil-safe: a nil *cacheTracer behaves as no-op.
type cacheTracer struct {
	tracer        trace.Tracer
	parentSpanCtx trace.SpanContext
}

// newCacheTracer returns a cacheTracer that shares the process-wide
// tracer provider. Returns nil when tracing is disabled (shared
// provider not initialized or provider setup failed); callers needn't
// check for nil before invoking methods.
func newCacheTracer(w io.Writer) *cacheTracer {
	if _, err := gotrace.Provider(context.Background()); err != nil {
		// Surface init failures once; downstream Start becomes a no-op.
		w.Write([]byte("cacheprog: otel init failed: " + err.Error() + "\n"))
		return nil
	}
	tracer := gotrace.Tracer("go-toolchain/cacheprog")
	if tracer == nil {
		return nil
	}
	return &cacheTracer{
		tracer:        tracer,
		parentSpanCtx: extractParentSpanContext(),
	}
}

// Start begins a span rooted at the propagated parent trace context. The
// returned context should be passed to any downstream spans so they nest
// properly. Nil-safe: returns a fresh background context and a noop span
// when tracing is disabled.
func (t *cacheTracer) Start(name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return t.StartFromCtx(nil, name, attrs...)
}

// StartFromCtx begins a span as a child of the span carried by ctx. When
// ctx is nil or carries no valid span, the run-level parent from
// OTEL_TRACEPARENT is used so the span still roots under the
// go-toolchain run. This is the entry point for nesting spans under a
// caller-provided parent — e.g. individual cache GETs that should appear
// as children of the batch span that issued them. Nil-safe: returns a
// noop span when tracing is disabled.
func (t *cacheTracer) StartFromCtx(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	if t == nil || t.tracer == nil {
		return noopTracer.Start(ctx, name)
	}
	if !trace.SpanFromContext(ctx).SpanContext().IsValid() && t.parentSpanCtx.IsValid() {
		ctx = trace.ContextWithSpanContext(ctx, t.parentSpanCtx)
	}
	return t.tracer.Start(ctx, name, trace.WithAttributes(attrs...))
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

// extractParentSpanContext parses OTEL_TRACEPARENT (W3C Trace Context)
// into a SpanContext suitable as a parent for cache-mode spans. Delegates
// to the shared trace package, which is the authoritative parser.
func extractParentSpanContext() trace.SpanContext {
	if os.Getenv("OTEL_TRACEPARENT") == "" {
		return trace.SpanContext{}
	}
	return gotrace.ExtractParentSpanContext()
}
