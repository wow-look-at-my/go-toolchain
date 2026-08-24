//go:build cosmo

package trace

import (
	"context"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// GOOS=cosmo ships without OTel export: grpc (needed by the HTTP exporter) has no cosmo build; spans go nowhere.
func newOTLPExporter(ctx context.Context) (sdktrace.SpanExporter, error) {
	return noopSpanExporter{}, nil
}

// noopSpanExporter drops every span batch on the floor.
type noopSpanExporter struct{}

func (noopSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	return nil
}

func (noopSpanExporter) Shutdown(ctx context.Context) error { return nil }
