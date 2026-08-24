//go:build !cosmo

package trace

import (
	"context"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Real OTLP/HTTP exporter, split behind a build tag: otlptracehttp pulls
// in grpc, whose unix tag falsely matches cosmo (no x/sys/unix port).
func newOTLPExporter(ctx context.Context) (sdktrace.SpanExporter, error) {
	return otlptracehttp.New(ctx)
}
