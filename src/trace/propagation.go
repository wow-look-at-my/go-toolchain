package trace

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// CreateRootSpanContext builds a W3C Trace Context string for subprocess propagation: "00-traceID-spanID-01".
func CreateRootSpanContext(traceID trace.TraceID, spanID trace.SpanID) string {
	return fmt.Sprintf("00-%s-%s-01", traceID.String(), spanID.String())
}

// ExtractParentSpanContext extracts W3C Trace Context from OTEL_TRACEPARENT env var
// and returns a SpanContext suitable for use as parent. Returns nil if not set or invalid.
func ExtractParentSpanContext() trace.SpanContext {
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

	traceFlags, err := strconv.ParseUint(parts[3], 16, 8)
	if err != nil {
		return trace.SpanContext{}
	}

	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.TraceFlags(traceFlags),
		Remote:     true,
	})
}

// ContextWithParentSpan injects a parent SpanContext into a context.Context.
func ContextWithParentSpan(ctx context.Context, parentSpan trace.SpanContext) context.Context {
	if !parentSpan.IsValid() {
		return ctx
	}
	return trace.ContextWithSpanContext(ctx, parentSpan)
}
