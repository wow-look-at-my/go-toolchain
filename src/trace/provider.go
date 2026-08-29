package trace

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

// Shared tracer-provider plumbing for every OTel emitter: a single batcher, a gzip'd HTTP POST per build.

var (
	providerOnce    sync.Once
	providerTP      *sdktrace.TracerProvider
	providerErr     error
	providerEnabled bool

	shutdownOnce sync.Once
)

// Provider returns the shared tracer provider, initializing it on the
// opening call if OTEL_EXPORTER_OTLP_ENDPOINT is set. Later calls reuse the
// same provider. Returns (nil, nil) when tracing is disabled — callers
// should treat a nil provider as "no-op" and use the noop tracer.
func Provider(ctx context.Context) (*sdktrace.TracerProvider, error) {
	providerOnce.Do(func() {
		if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
			return
		}
		providerTP, providerErr = buildProvider(ctx)
		if providerErr == nil {
			providerEnabled = true
		}
	})
	return providerTP, providerErr
}

// IsEnabled reports whether the shared provider was successfully set up.
func IsEnabled() bool { return providerEnabled }

// Shutdown flushes and tears down the shared provider. Idempotent and
// safe to call even if the provider was never initialized. Should be
// called a single time at the end of a build.
func Shutdown(ctx context.Context) error {
	var err error
	shutdownOnce.Do(func() {
		if providerTP == nil {
			return
		}
		flushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if ferr := providerTP.ForceFlush(flushCtx); ferr != nil {
			err = ferr
		}
		if serr := providerTP.Shutdown(flushCtx); serr != nil && err == nil {
			err = serr
		}
	})
	return err
}

func buildProvider(ctx context.Context) (*sdktrace.TracerProvider, error) {
	// Exporter construction is build-tag split: see provider_otlp.go and provider_otlp_cosmo.go.
	otlp, err := newOTLPExporter(ctx)
	if err != nil {
		return nil, err
	}
	res, err := buildProviderResource(ctx)
	if err != nil {
		return nil, err
	}

	exporter := &loggingExporter{inner: otlp, w: os.Stderr}

	opts := []sdktrace.TracerProviderOption{
		// A single batcher for the whole build: extend the default timeout
		// so a normal build emits very few exports total, and grow the
		// queue/batch to fit the flood of cacheprog spans.
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(30*time.Second),
			sdktrace.WithMaxQueueSize(8192),
			sdktrace.WithMaxExportBatchSize(2048),
		),
		sdktrace.WithResource(res),
	}

	// If OTEL_TRACEPARENT names a root spanID, force our opening root span to adopt it, so
	// cacheprog's already-emitted spans have a real parent.
	if sc := ExtractParentSpanContext(); sc.IsValid() {
		opts = append(opts, sdktrace.WithIDGenerator(newRootIDGenerator(sc.TraceID(), sc.SpanID())))
	}

	return sdktrace.NewTracerProvider(opts...), nil
}

func buildProviderResource(ctx context.Context) (*resource.Resource, error) {
	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "go-toolchain"
	}
	attrs := []attribute.KeyValue{semconv.ServiceName(serviceName)}
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

// loggingExporter wraps an OTLP SpanExporter and logs a line per export: the span count
// per HTTP POST batch, not per span.
type loggingExporter struct {
	inner sdktrace.SpanExporter
	w     interface {
		Write(p []byte) (n int, err error)
	}
	count atomic.Uint64
}

func (e *loggingExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	start := time.Now()
	err := e.inner.ExportSpans(ctx, spans)
	batchNum := e.count.Add(1)
	status := "ok"
	if err != nil {
		status = err.Error()
	}
	fmt.Fprintf(e.w, "otel: batch #%d exported %d spans in %v (%s)\n",
		batchNum, len(spans), time.Since(start).Round(time.Millisecond), status)
	return err
}

func (e *loggingExporter) Shutdown(ctx context.Context) error {
	return e.inner.Shutdown(ctx)
}

// Tracer returns a tracer from the shared provider, or nil if the provider isn't initialized.
func Tracer(name string) trace.Tracer {
	if providerTP == nil {
		return nil
	}
	return providerTP.Tracer(name)
}

// rootIDGenerator returns a fixed (traceID, spanID) a single time, then random IDs after, so the
// opening root span can adopt a given ID.
type rootIDGenerator struct {
	traceID trace.TraceID
	spanID  trace.SpanID
	used    atomic.Bool
}

func newRootIDGenerator(traceID trace.TraceID, spanID trace.SpanID) *rootIDGenerator {
	return &rootIDGenerator{traceID: traceID, spanID: spanID}
}

func (g *rootIDGenerator) NewIDs(ctx context.Context) (trace.TraceID, trace.SpanID) {
	if g.used.CompareAndSwap(false, true) {
		return g.traceID, g.spanID
	}
	var tid [16]byte
	var sid [8]byte
	rand.Read(tid[:])
	rand.Read(sid[:])
	return trace.TraceID(tid), trace.SpanID(sid)
}

func (g *rootIDGenerator) NewSpanID(ctx context.Context, traceID trace.TraceID) trace.SpanID {
	var sid [8]byte
	rand.Read(sid[:])
	return trace.SpanID(sid)
}
