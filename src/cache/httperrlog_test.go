package cache

import (
	"bytes"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wow-look-at-my/testify/require"
)

// newTestLogger returns a logger with a long flush interval so that all
// flushing happens via Close (deterministic for tests).
func newTestLogger(buf *bytes.Buffer) *httpErrLogger {
	return newHTTPErrLogger(buf, time.Hour)
}

func TestHTTPErrLogger_SingleRecordFormat(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	l.Record("web put", 502, "8187a9f3deadbeef", "error code: 502")
	require.NoError(t, l.Close())

	require.Equal(t, "cacheprog: web put 8187a9f3: HTTP 502: error code: 502\n", buf.String())
}

func TestHTTPErrLogger_CoalesceSameKey(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	ids := []string{
		"hash0001abcdef00", "hash0002abcdef00", "hash0003abcdef00",
		"hash0004abcdef00", "hash0005abcdef00",
	}
	for _, id := range ids {
		l.Record("web put", 502, id, "error code: 502")
	}
	require.NoError(t, l.Close())

	out := buf.String()
	require.Equal(t, 1, strings.Count(out, "\n"), "expected exactly one line, got: %q", out)
	require.Contains(t, out, "[hash0001, hash0002, hash0003, and 2 more]")
	require.Contains(t, out, "HTTP 502: error code: 502")
}

func TestHTTPErrLogger_CoalesceUnderMaxNamed(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	l.Record("web put", 502, "aaaa1111", "boom")
	l.Record("web put", 502, "bbbb2222", "boom")
	require.NoError(t, l.Close())

	require.Equal(t, "cacheprog: web put [aaaa1111, bbbb2222]: HTTP 502: boom\n", buf.String())
}

func TestHTTPErrLogger_DifferentKeysStayDistinct(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	l.Record("web put", 502, "aaaaaaaaaaaa", "error code: 502")
	l.Record("web put", 503, "bbbbbbbbbbbb", "error code: 502")
	l.Record("web get", 502, "ccccccccccccc", "error code: 502")
	l.Record("web put", 502, "ddddddddddddd", "different body")
	require.NoError(t, l.Close())

	out := buf.String()
	require.Equal(t, 4, strings.Count(out, "\n"), "expected 4 lines, got: %q", out)
}

func TestHTTPErrLogger_EmptyBodyOmitsTrailer(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	l.Record("web batch get", 502, "c5061394aabbccdd", "")
	require.NoError(t, l.Close())

	require.Equal(t, "cacheprog: web batch get c5061394: HTTP 502\n", buf.String())
}

func TestHTTPErrLogger_BodyNormalization(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	// Five records with body variations that all normalize to the same key.
	// Five > maxNamed=3, so we get an "and N more" tail and can verify
	// the count reflects every record (including the whitespace variants).
	l.Record("web put", 502, "aaaa1111", "error code: 502")
	l.Record("web put", 502, "bbbb2222", "  error code: 502  ")
	l.Record("web put", 502, "cccc3333", "\nerror code: 502\n")
	l.Record("web put", 502, "dddd4444", "error code: 502")
	l.Record("web put", 502, "eeee5555", "error code: 502")
	require.NoError(t, l.Close())

	out := buf.String()
	require.Equal(t, 1, strings.Count(out, "\n"), "expected coalesced output, got: %q", out)
	require.Contains(t, out, "and 2 more")
}

func TestHTTPErrLogger_CloseFlushesPending(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	l.Record("web put", 502, "aabbccdd", "boom")
	require.Empty(t, buf.String(), "no flush should have happened before Close")
	require.NoError(t, l.Close())
	require.NotEmpty(t, buf.String(), "Close should flush pending records")
}

func TestHTTPErrLogger_CloseIdempotent(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	l.Record("web put", 502, "aabbccdd", "boom")
	require.NoError(t, l.Close())
	require.NotPanics(t, func() { _ = l.Close() })
}

func TestHTTPErrLogger_TickerFlush(t *testing.T) {
	var buf bytes.Buffer
	l := newHTTPErrLogger(&buf, 10*time.Millisecond)

	l.Record("web put", 502, "aabbccdd", "boom")

	require.Eventually(t, func() bool {
		return buf.Len() > 0
	}, time.Second, 5*time.Millisecond, "ticker should have flushed")

	require.NoError(t, l.Close())
}

func TestHTTPErrLogger_ConcurrentRecord(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	const goroutines = 50
	const perGoroutine = 100
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				l.Record("web put", 502, "aabbccdd", "error code: 502")
			}
		}()
	}
	wg.Wait()
	require.NoError(t, l.Close())

	out := buf.String()
	require.Equal(t, 1, strings.Count(out, "\n"), "expected one coalesced line, got: %q", out)
	expected := goroutines*perGoroutine - httpErrMaxNamed
	require.Contains(t, out, "and "+strconv.Itoa(expected)+" more")
}

func TestHTTPErrLogger_NilReceiver(t *testing.T) {
	var l *httpErrLogger
	require.NotPanics(t, func() {
		l.Record("web put", 502, "aabbccdd", "boom")
		l.Record("web batch get", 502, "ccddeeff", "")
	})
}

func TestHTTPErrLogger_ShortIDSafe(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	require.NotPanics(t, func() {
		l.Record("web put", 502, "ab12", "boom")
	})
	require.NoError(t, l.Close())
	require.Contains(t, buf.String(), "ab12")
}

func TestHTTPErrLogger_OTELOptOutByDefault(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	require.Nil(t, l.tracer, "tracer must be nil when OTEL endpoint is unset")
	require.Nil(t, l.tp, "tracer provider must be nil when OTEL endpoint is unset")
	require.NoError(t, l.Close())
}

func TestHTTPErrLogger_BatchInfoCoalesced(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	// 10 zero-entry batch GETs — the flooded case from the user's report.
	ids := []string{
		"823cb0ef0000", "d505433f0000", "2307a01d0000", "cd5e3e1f0000",
		"7238368d0000", "ed517f440000", "c7ccde380000", "e2d8d6d80000",
		"487f399a0000", "aabbccdd0000",
	}
	for i, id := range ids {
		l.RecordBatchInfo(id, 0, 0, time.Duration(30+i%2)*time.Millisecond)
	}
	require.NoError(t, l.Close())

	out := buf.String()
	require.Equal(t, 1, strings.Count(out, "\n"), "expected one aggregated line, got: %q", out)
	require.Contains(t, out, "cacheprog: 10 batch get misses [")
	require.Contains(t, out, "823cb0ef, d505433f, 2307a01d, and 7 more")
	require.Contains(t, out, "30-31ms per request")
	require.Contains(t, out, "server has no entries for these keys")
}

func TestHTTPErrLogger_BatchInfoCoalescedHits(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	// Hit case: server returned 5 entries (4 prefetch + 1 requested) per request.
	l.RecordBatchInfo("aaaa1111", 5, 4, 47*time.Millisecond)
	l.RecordBatchInfo("bbbb2222", 5, 4, 50*time.Millisecond)
	require.NoError(t, l.Close())

	out := buf.String()
	require.Equal(t, 1, strings.Count(out, "\n"), "expected one aggregated line, got: %q", out)
	require.Contains(t, out, "cacheprog: 2 batch gets [aaaa1111, bbbb2222]")
	require.Contains(t, out, "each returned 5 entries (4 prefetched) in 47-50ms")
}

func TestHTTPErrLogger_BatchInfoSingleRecord(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	l.RecordBatchInfo("abcd1234deadbeef", 5, 2, 47*time.Millisecond)
	require.NoError(t, l.Close())

	require.Equal(t, "cacheprog: batch get abcd1234: 5 entries (2 prefetched) in 47ms\n", buf.String())
}

func TestHTTPErrLogger_BatchInfoSingleMiss(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	l.RecordBatchInfo("abcd1234deadbeef", 0, 0, 30*time.Millisecond)
	require.NoError(t, l.Close())

	require.Equal(t, "cacheprog: batch get abcd1234: miss (server returned no entries) in 30ms\n", buf.String())
}

func TestHTTPErrLogger_BatchInfoDifferentShapesStayDistinct(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	l.RecordBatchInfo("aaaa1111", 0, 0, 30*time.Millisecond)
	l.RecordBatchInfo("bbbb2222", 0, 0, 30*time.Millisecond)
	l.RecordBatchInfo("cccc3333", 5, 2, 47*time.Millisecond)
	l.RecordBatchInfo("dddd4444", 5, 0, 47*time.Millisecond)
	require.NoError(t, l.Close())

	out := buf.String()
	require.Equal(t, 3, strings.Count(out, "\n"), "expected 3 distinct lines, got: %q", out)
}

func TestHTTPErrLogger_BatchInfoDurationRange(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	l.RecordBatchInfo("aaaa1111", 0, 0, 20*time.Millisecond)
	l.RecordBatchInfo("bbbb2222", 0, 0, 100*time.Millisecond)
	l.RecordBatchInfo("cccc3333", 0, 0, 50*time.Millisecond)
	require.NoError(t, l.Close())

	require.Contains(t, buf.String(), "20-100ms per request")
}

func TestHTTPErrLogger_BatchInfoSingleDurationNoRange(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	l.RecordBatchInfo("aaaa1111", 0, 0, 30*time.Millisecond)
	l.RecordBatchInfo("bbbb2222", 0, 0, 30*time.Millisecond)
	require.NoError(t, l.Close())

	out := buf.String()
	require.Contains(t, out, "30ms per request")
	require.NotContains(t, out, "30-30")
}

func TestHTTPErrLogger_BatchInfoNilReceiver(t *testing.T) {
	var l *httpErrLogger
	require.NotPanics(t, func() {
		l.RecordBatchInfo("aabbccdd", 0, 0, 30*time.Millisecond)
	})
}

func TestHTTPErrLogger_MixedHTTPErrAndBatchInfo(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	l.Record("web put", 502, "aaaaaaaa", "error code: 502")
	l.RecordBatchInfo("bbbbbbbb", 0, 0, 30*time.Millisecond)
	require.NoError(t, l.Close())

	out := buf.String()
	require.Equal(t, 2, strings.Count(out, "\n"), "expected both groups flushed, got: %q", out)
	require.Contains(t, out, "HTTP 502")
	require.Contains(t, out, "miss (server returned no entries)")
}

func TestHTTPErrLogger_NoFlushWhenEmpty(t *testing.T) {
	var buf bytes.Buffer
	l := newHTTPErrLogger(&buf, 5*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	require.Empty(t, buf.String(), "ticker must not emit when there are no records")
	require.NoError(t, l.Close())
}
