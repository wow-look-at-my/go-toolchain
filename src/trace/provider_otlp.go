//go:build !cosmo

package trace

import (
	"context"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Real exporter, split out: otlptracehttp pulls in grpc, whose unix tag falsely matches cosmo.
func newOTLPExporter(ctx context.Context) (sdktrace.SpanExporter, error) {
	return otlptracehttp.New(ctx)
}
