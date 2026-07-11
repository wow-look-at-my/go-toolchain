//go:build !cosmo

package trace

import (
	"context"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// newOTLPExporter returns the real OTLP/HTTP span exporter. Split behind a
// build tag because otlptracehttp's internal otlpconfig imports
// google.golang.org/grpc even for the pure-HTTP exporter (a known upstream
// issue, still present at otel v1.44.0), and grpc's //go:build unix files
// match GOOS=cosmo while golang.org/x/sys/unix has no cosmo port.
// provider_otlp_cosmo.go supplies the cosmo fallback.
func newOTLPExporter(ctx context.Context) (sdktrace.SpanExporter, error) {
	return otlptracehttp.New(ctx)
}
