package cache

import (
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"time"
)

// Failure-handling defaults for the remote cache backend. They are deliberately
// conservative: the cache is an optimization, never a correctness dependency, so
// the priority under any backend trouble is to get out of the way of the build
// fast and quietly rather than to keep trying.
const (
	// defaultMaxRetries bounds how many extra attempts a transient failure gets.
	// Small on purpose — retries against a struggling backend add to the very
	// load that is hurting it. Retries honor the server's Retry-After (which is
	// "wait," not "give up") and paper over isolated single-request blips; a
	// genuine failure that outlasts the retry budget simply falls back to a local
	// miss for that one operation, without disabling the remote tier.
	defaultMaxRetries = 2

	// defaultEmptyBatchBackoff is the number of consecutive zero-entry /_batch/get
	// responses that disables further batch probing for the rest of the process.
	// An empty-but-200 batch is a HEALTHY backend that simply has none of this
	// build's keys. CI batches were ~1.6 keys / ~45ms each, so backing off after
	// ~24 consecutive empties bounds the wasted probing to ~1s; the daemon is one
	// process for the whole build, so one trip covers every later phase. 0
	// disables the backoff (override via GO_TOOLCHAIN_CACHE_EMPTY_BATCH_BACKOFF).
	defaultEmptyBatchBackoff = 24

	// retryBaseDelay / retryMaxDelay bound the exponential backoff between
	// retries. Full jitter is applied on top (see sleepBackoff).
	retryBaseDelay = 100 * time.Millisecond
	retryMaxDelay  = 2 * time.Second
)

// noteBatchEntries feeds the entry count of one /_batch/get 200 response to the
// consecutive-empty-batch backoff. A zero-entry batch is a healthy remote that
// holds none of this build's keys; once enough of them stack up, the remote has
// demonstrably nothing useful for this run, so we disable further batch probing
// (logged once). Any non-empty batch resets the streak — the remote IS serving.
// An empty-but-200 response is not a backend failure: the backoff is purely a
// "nothing here to fetch" optimization, orthogonal to the per-op retry path.
func (b *WebBackend) noteBatchEntries(n int) {
	if b.emptyBatchBackoffThreshold <= 0 || b.batchProbingDisabled.Load() {
		return
	}
	if n > 0 {
		b.consecutiveEmptyBatches.Store(0)
		return
	}
	if b.consecutiveEmptyBatches.Add(1) >= int64(b.emptyBatchBackoffThreshold) {
		if b.batchProbingDisabled.CompareAndSwap(false, true) {
			b.batchBackoffLogOnce.Do(func() {
				fmt.Fprintf(os.Stderr, "cacheprog: remote returned %d empty batches; "+
					"disabling further batch probes for this run (endpoint=%s)\n",
					b.emptyBatchBackoffThreshold, b.endpoint)
			})
		}
	}
}

// batchProbingOff reports whether the consecutive-empty-batch backoff has
// tripped, meaning cold not-in-index keys should miss cleanly without a
// /_batch/get round-trip for the rest of this run.
func (b *WebBackend) batchProbingOff() bool {
	return b.batchProbingDisabled.Load()
}

// envInt reads an integer environment variable, falling back to def when unset
// or unparseable. A negative value is clamped to 0 (feature disabled).
func envInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	if n < 0 {
		return 0
	}
	return n
}

// transientStatus reports whether an HTTP status code indicates a transient
// backend problem worth retrying. 5xx (gateway/upstream trouble — the 502 storm)
// and 429 (overload) are transient; every 4xx, including a 404 cache miss, is a
// definitive answer from a healthy backend and must NOT be retried.
func transientStatus(code int) bool {
	return code >= 500 || code == http.StatusTooManyRequests
}

// parseRetryAfter extracts a backoff hint from a response's Retry-After header.
// It handles the delta-seconds form (an integer number of seconds) and the
// HTTP-date form, returning 0 when the header is absent or unparseable. The
// result is capped at retryMaxDelay so a server cannot pin a retry far into the
// future.
func parseRetryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0
	}
	var d time.Duration
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		d = time.Duration(secs) * time.Second
	} else if t, err := http.ParseTime(v); err == nil {
		d = time.Until(t)
		if d <= 0 {
			return 0
		}
	} else {
		return 0
	}
	if d > retryMaxDelay {
		d = retryMaxDelay
	}
	return d
}

// doRetryGET issues an idempotent GET (individual get, batch get) with the
// configured number of bounded retries on transient failures. The index fetch
// uses doRetryGETN directly with its own tighter budget.
func (b *WebBackend) doRetryGET(req *http.Request) (*http.Response, error) {
	return b.doRetry(req, b.maxRetries)
}

// doRetryGETN issues an idempotent GET with up to maxRetries retries — the
// index fetch caps its retries below the configured policy so a slow server
// cannot stall daemon startup (see indexFetchBudget / indexFetchRetries).
func (b *WebBackend) doRetryGETN(req *http.Request, maxRetries int) (*http.Response, error) {
	return b.doRetry(req, maxRetries)
}

// doRetryPUT issues an upload with the same bounded-retry/backoff policy as
// doRetryGET. PUT is idempotent for this cache (the key is the content address,
// so re-storing the same object is a no-op), which makes retrying a shed
// request safe. The body is supplied as an in-memory []byte so each attempt can
// rebuild a fresh reader via req.GetBody — without this a 503 admission shed
// (the cache server's backpressure under a CI burst) silently dropped the
// upload and the object was never stored.
func (b *WebBackend) doRetryPUT(req *http.Request, body []byte) (*http.Response, error) {
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	return b.doRetry(req, b.maxRetries)
}

// doRetry is the shared retry loop behind doRetryGET, doRetryGETN, and
// doRetryPUT. It retries a transient response (transientStatus) up to
// maxRetries times, sleeping max(exponential-jittered backoff, server
// Retry-After) capped at retryMaxDelay between attempts, and rewinds the
// request body from req.GetBody on each retry. It returns the final
// (resp, err) exactly as http.Client.Do would, so callers handle status codes
// and bodies unchanged; it never retries a definitive (<500, non-429)
// response. A 503 admission shed is transient and so is retried and backed
// off (honoring Retry-After); if the retry budget is exhausted the caller
// falls back to a local miss for that one operation.
func (b *WebBackend) doRetry(req *http.Request, maxRetries int) (*http.Response, error) {
	var (
		resp *http.Response
		err  error
	)
	for attempt := 0; ; attempt++ {
		// Rewind the body for a retry (batch get carries a small JSON body; a
		// PUT carries the compressed object).
		if attempt > 0 && req.GetBody != nil {
			if body, gerr := req.GetBody(); gerr == nil {
				req.Body = body
			}
		}
		resp, err = b.client.Do(req)
		if err == nil && !transientStatus(resp.StatusCode) {
			return resp, nil
		}
		if attempt >= maxRetries {
			return resp, err
		}
		// Honor a server-supplied Retry-After (e.g. the admission-control 503
		// shed), but never sleep less than the jittered backoff.
		var retryAfter time.Duration
		if err == nil {
			retryAfter = parseRetryAfter(resp)
			// Drain and close a transient response before retrying so the
			// connection returns to the pool.
			io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
			resp.Body.Close()
		}
		b.sleepBackoff(attempt, retryAfter)
	}
}

// sleepBackoff waits before a retry, returning early if the backend is shutting
// down. The base wait is an exponentially increasing, fully jittered delay
// (full jitter — a random duration in [0, cap]) so a parallel build does not
// synchronize into a thundering herd against a recovering backend. A non-zero
// atLeast (a server Retry-After hint) raises the floor: the actual sleep is
// max(jittered backoff, atLeast), still capped at retryMaxDelay.
func (b *WebBackend) sleepBackoff(attempt int, atLeast time.Duration) {
	d := retryBaseDelay << attempt
	if d > retryMaxDelay || d <= 0 {
		d = retryMaxDelay
	}
	d = time.Duration(rand.Int64N(int64(d) + 1))
	if atLeast > 0 {
		if atLeast > retryMaxDelay {
			atLeast = retryMaxDelay
		}
		if atLeast > d {
			d = atLeast
		}
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-b.batchStop:
	}
}
