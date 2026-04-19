package cache

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
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

	mu        sync.Mutex
	w         io.Writer
	interval  time.Duration
	maxNamed  int
	groups    map[httpErrKey]*httpErrGroup
	batchHTTP map[batchHTTPKey]*batchHTTPGroup

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

// batchHTTPKey buckets batch-GET HTTP requests so all-miss requests are
// reported separately from requests that hit something. Counts/durations
// vary across requests in the same bucket and are reported as ranges.
type batchHTTPKey struct {
	allMiss bool
}

type batchHTTPGroup struct {
	total      int // number of HTTP requests in this bucket
	minKeys    int
	maxKeys    int
	minEntries int
	maxEntries int
	minPref    int
	maxPref    int
	minDur     time.Duration
	maxDur     time.Duration
}

// newHTTPErrLogger returns a logger that writes aggregated stderr summaries
// to w on every interval tick (and once more on Close). If
// OTEL_EXPORTER_OTLP_ENDPOINT is set, errors are also exported as
// cacheprog.http_error spans. Init failures fall back to stderr-only mode.
func newHTTPErrLogger(w io.Writer, interval time.Duration) *httpErrLogger {
	l := &httpErrLogger{
		w:         w,
		interval:  interval,
		maxNamed:  httpErrMaxNamed,
		groups:    map[httpErrKey]*httpErrGroup{},
		batchHTTP: map[batchHTTPKey]*batchHTTPGroup{},
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
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

// RecordBatchHTTP coalesces stats from one batch-GET HTTP request
// (which may carry many keys after client-side coalescing). Buckets are
// split by hit/all-miss so a flaky cold cache stays distinguishable from
// a working one. Nil-safe.
func (l *httpErrLogger) RecordBatchHTTP(keysRequested, entriesReturned, prefetched int, dur time.Duration) {
	if l == nil {
		fmt.Fprintf(os.Stderr, "cacheprog: batch GET: %d keys → %d entries (%d prefetched) in %v\n",
			keysRequested, entriesReturned, prefetched, dur.Round(time.Millisecond))
		return
	}
	key := batchHTTPKey{allMiss: entriesReturned == 0}
	l.mu.Lock()
	g, ok := l.batchHTTP[key]
	if !ok {
		g = &batchHTTPGroup{
			minKeys: keysRequested, maxKeys: keysRequested,
			minEntries: entriesReturned, maxEntries: entriesReturned,
			minPref: prefetched, maxPref: prefetched,
			minDur: dur, maxDur: dur,
		}
		l.batchHTTP[key] = g
	} else {
		if keysRequested < g.minKeys {
			g.minKeys = keysRequested
		}
		if keysRequested > g.maxKeys {
			g.maxKeys = keysRequested
		}
		if entriesReturned < g.minEntries {
			g.minEntries = entriesReturned
		}
		if entriesReturned > g.maxEntries {
			g.maxEntries = entriesReturned
		}
		if prefetched < g.minPref {
			g.minPref = prefetched
		}
		if prefetched > g.maxPref {
			g.maxPref = prefetched
		}
		if dur < g.minDur {
			g.minDur = dur
		}
		if dur > g.maxDur {
			g.maxDur = dur
		}
	}
	g.total++
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
	if len(l.groups) == 0 && len(l.batchHTTP) == 0 {
		l.mu.Unlock()
		return
	}
	groups := l.groups
	batchHTTP := l.batchHTTP
	l.groups = map[httpErrKey]*httpErrGroup{}
	l.batchHTTP = map[batchHTTPKey]*batchHTTPGroup{}
	l.mu.Unlock()
	for k, g := range groups {
		fmt.Fprintln(l.w, formatGroup(k, g))
	}
	for k, g := range batchHTTP {
		fmt.Fprintln(l.w, formatBatchHTTPGroup(k, g))
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
	ids := formatIDList(g.named, g.total)
	if g.bodyRaw == "" {
		return fmt.Sprintf("cacheprog: %s %s: HTTP %d", k.op, ids, k.status)
	}
	return fmt.Sprintf("cacheprog: %s %s: HTTP %d: %s", k.op, ids, k.status, g.bodyRaw)
}

func formatBatchHTTPGroup(k batchHTTPKey, g *batchHTTPGroup) string {
	keysS := intRangeStr(g.minKeys, g.maxKeys)
	durS := durRangeStr(g.minDur, g.maxDur)

	if g.total == 1 {
		if k.allMiss {
			return fmt.Sprintf("cacheprog: batch GET: %s keys → 0 entries (server has no entries for any of them) in %s",
				keysS, durS)
		}
		return fmt.Sprintf("cacheprog: batch GET: %s keys → %s entries (%s prefetched) in %s",
			keysS,
			intRangeStr(g.minEntries, g.maxEntries),
			intRangeStr(g.minPref, g.maxPref),
			durS)
	}

	if k.allMiss {
		return fmt.Sprintf("cacheprog: batch GET ×%d: %s keys → 0 entries (server has no entries) per request, %s each",
			g.total, keysS, durS)
	}
	return fmt.Sprintf("cacheprog: batch GET ×%d: %s keys → %s entries (%s prefetched) per request, %s each",
		g.total, keysS,
		intRangeStr(g.minEntries, g.maxEntries),
		intRangeStr(g.minPref, g.maxPref),
		durS)
}

func intRangeStr(min, max int) string {
	if min == max {
		return strconv.Itoa(min)
	}
	return fmt.Sprintf("%d-%d", min, max)
}

func durRangeStr(min, max time.Duration) string {
	minMs := min.Round(time.Millisecond).Milliseconds()
	maxMs := max.Round(time.Millisecond).Milliseconds()
	if minMs == maxMs {
		return fmt.Sprintf("%dms", minMs)
	}
	return fmt.Sprintf("%d-%dms", minMs, maxMs)
}

// formatIDList renders the named IDs + "and N more" tail (or a bare
// single ID if total == 1). Shared by formatGroup and formatBatchGroup.
func formatIDList(named []string, total int) string {
	if total == 1 && len(named) == 1 {
		return named[0]
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, n := range named {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(n)
	}
	if total > len(named) {
		fmt.Fprintf(&b, ", and %d more", total-len(named))
	}
	b.WriteByte(']')
	return b.String()
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
