//go:build cosmo

package trace

import (
	"context"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// TODO(cosmo): GOOS=cosmo builds currently ship WITHOUT OTel export — a real,
// temporarily accepted regression. The OTLP/HTTP exporter cannot compile for
// cosmo: otlptracehttp's internal otlpconfig unconditionally imports
// google.golang.org/grpc (even though the HTTP exporter never dials gRPC),
// and grpc's //go:build unix files match cosmo while golang.org/x/sys/unix
// has no cosmo port. Until either (a) otel drops the grpc dependency from
// the HTTP exporter, (b) x/sys/unix grows a cosmo port, or (c) we hand-roll
// a minimal OTLP/HTTP client, cosmo builds keep the full tracing API surface
// (provider, tracers, propagation, span IDs) but export spans to nowhere.
func newOTLPExporter(ctx context.Context) (sdktrace.SpanExporter, error) {
	return noopSpanExporter{}, nil
}

// noopSpanExporter drops every span batch on the floor.
type noopSpanExporter struct{}

func (noopSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	return nil
}

func (noopSpanExporter) Shutdown(ctx context.Context) error { return nil }
