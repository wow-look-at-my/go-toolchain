package cache

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	httpErrFlushInterval = 30 * time.Second
	httpErrMaxNamed      = 3
	httpErrBodyKeyLen    = 128
	httpErrBodyDispLen   = 512
	httpErrShortIDLen    = 8
)

// httpErrLogger coalesces repetitive HTTP error messages from the web
// backend so that a failing remote doesn't flood stderr with one line
// per request. Each error is also exported as a short OTel span when
// OTEL_EXPORTER_OTLP_ENDPOINT is set, so the granular per-request data
// is preserved out-of-band.
type httpErrLogger struct {
	tp     *sdktrace.TracerProvider // nil if OTel is not configured
	tracer trace.Tracer             // nil if tp is nil

	mu       sync.Mutex
	w        io.Writer
	interval time.Duration
	maxNamed int
	groups   map[httpErrKey]*httpErrGroup

	stop   chan struct{}
	done   chan struct{}
	closed bool
}

type httpErrKey struct {
	op       string // "web put" | "web get" | "web batch get"
	status   int
	bodyNorm string // normalized body for dedup stability
}

type httpErrGroup struct {
	named   []string // first maxNamed short IDs, in order seen
	total   int      // total records matching this key
	bodyRaw string   // last-observed raw body, for display
}

// newHTTPErrLogger returns a logger that writes aggregated stderr summaries
// to w on every interval tick (and once more on Close). If
// OTEL_EXPORTER_OTLP_ENDPOINT is set, errors are also exported as
// cacheprog.http_error spans. Init failures fall back to stderr-only mode.
func newHTTPErrLogger(w io.Writer, interval time.Duration) *httpErrLogger {
	l := &httpErrLogger{
		w:        w,
		interval: interval,
		maxNamed: httpErrMaxNamed,
		groups:   map[httpErrKey]*httpErrGroup{},
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if tp, err := newCacheTracerProvider(ctx); err != nil {
			fmt.Fprintf(w, "cacheprog: otel init failed: %v\n", err)
		} else {
			l.tp = tp
			l.tracer = tp.Tracer("go-toolchain/cacheprog")
		}
	}
	go l.loop()
	return l
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

// Record reports one HTTP error: emits an OTel span (if configured) and
// queues the error for the next stderr summary flush. Safe for nil receiver
// so call sites that may have a partially-constructed WebBackend don't panic.
func (l *httpErrLogger) Record(op string, status int, id, body string) {
	if l == nil {
		fmt.Fprintf(os.Stderr, "cacheprog: %s %s: HTTP %d", op, shortID(id), status)
		if body != "" {
			fmt.Fprintf(os.Stderr, ": %s", body)
		}
		fmt.Fprintln(os.Stderr)
		return
	}
	if l.tracer != nil {
		l.emitSpan(op, status, id, body)
	}
	key := httpErrKey{
		op:       op,
		status:   status,
		bodyNorm: normalizeBody(body),
	}
	l.mu.Lock()
	g, ok := l.groups[key]
	if !ok {
		g = &httpErrGroup{}
		l.groups[key] = g
	}
	g.total++
	if len(g.named) < l.maxNamed {
		g.named = append(g.named, shortID(id))
	}
	g.bodyRaw = body
	l.mu.Unlock()
}

func (l *httpErrLogger) emitSpan(op string, status int, id, body string) {
	attrs := []attribute.KeyValue{
		attribute.String("cacheprog.op", op),
		attribute.Int("http.response.status_code", status),
		attribute.String("cacheprog.action_id", shortID(id)),
	}
	if body != "" {
		disp := body
		if len(disp) > httpErrBodyDispLen {
			disp = disp[:httpErrBodyDispLen]
		}
		attrs = append(attrs, attribute.String("cacheprog.body", disp))
	}
	_, span := l.tracer.Start(context.Background(), "cacheprog.http_error",
		trace.WithAttributes(attrs...),
	)
	span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d", status))
	span.End()
}

func (l *httpErrLogger) loop() {
	defer close(l.done)
	t := time.NewTicker(l.interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			l.flush()
		case <-l.stop:
			l.flush()
			return
		}
	}
}

func (l *httpErrLogger) flush() {
	l.mu.Lock()
	if len(l.groups) == 0 {
		l.mu.Unlock()
		return
	}
	groups := l.groups
	l.groups = map[httpErrKey]*httpErrGroup{}
	l.mu.Unlock()
	for k, g := range groups {
		fmt.Fprintln(l.w, formatGroup(k, g))
	}
}

// Close stops the background ticker, flushes any pending groups, and shuts
// down the OTel exporter. Idempotent.
func (l *httpErrLogger) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	l.mu.Unlock()

	close(l.stop)
	<-l.done
	l.flush()

	if l.tp != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = l.tp.ForceFlush(ctx)
		_ = l.tp.Shutdown(ctx)
	}
	return nil
}

func formatGroup(k httpErrKey, g *httpErrGroup) string {
	var ids string
	if g.total == 1 {
		ids = g.named[0]
	} else {
		var b strings.Builder
		b.WriteByte('[')
		for i, n := range g.named {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(n)
		}
		if g.total > len(g.named) {
			fmt.Fprintf(&b, ", and %d more", g.total-len(g.named))
		}
		b.WriteByte(']')
		ids = b.String()
	}
	if g.bodyRaw == "" {
		return fmt.Sprintf("cacheprog: %s %s: HTTP %d", k.op, ids, k.status)
	}
	return fmt.Sprintf("cacheprog: %s %s: HTTP %d: %s", k.op, ids, k.status, g.bodyRaw)
}

func normalizeBody(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > httpErrBodyKeyLen {
		s = s[:httpErrBodyKeyLen]
	}
	return s
}

func shortID(id string) string {
	if len(id) > httpErrShortIDLen {
		return id[:httpErrShortIDLen]
	}
	return id
}
