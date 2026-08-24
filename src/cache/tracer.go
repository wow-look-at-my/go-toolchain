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

// noopTracer lets Start's ctx/span idiom skip nil guards; trace.Span is sealed, so this can't be stubbed.
var noopTracer = noop.NewTracerProvider().Tracer("noop")

// cacheTracer roots spans under the run's OTEL_TRACEPARENT via the shared provider; nil-safe (nil == no-op).
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

// Start begins a span rooted at the parent trace context; pass the returned ctx to children so they nest. Nil-safe.
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

// Enabled reports whether OTel tracing is configured, so callers can guard expensive attribute sets.
func (t *cacheTracer) Enabled() bool {
	return t != nil && t.tracer != nil
}

// markSpanMiss tags a cache-miss span with a structured reason; `cacheprog.miss_reason` is the primary diagnostic filter -- keep reason strings stable.
func markSpanMiss(span trace.Span, reason string) {
	span.SetAttributes(
		attribute.Bool("cacheprog.miss", true),
		attribute.String("cacheprog.miss_reason", reason),
	)
}

// markSpanErr records err and marks the span failed at stage; err must be non-nil (use SetStatus directly otherwise).
func markSpanErr(span trace.Span, stage string, err error) {
	span.RecordError(err)
	span.SetStatus(codes.Error, stage)
}

// extractParentSpanContext parses OTEL_TRACEPARENT (W3C Trace Context) into a parent SpanContext, delegating to the shared trace package.
func extractParentSpanContext() trace.SpanContext {
	if os.Getenv("OTEL_TRACEPARENT") == "" {
		return trace.SpanContext{}
	}
	return gotrace.ExtractParentSpanContext()
}
