package cache

import (
	"bytes"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// Failure-handling defaults for the remote cache backend. They are deliberately
// conservative: the cache is an optimization, never a correctness dependency, so
// the priority under any backend trouble is to get out of the way of the build
// fast and quietly rather than to keep trying.
const (
	// defaultMaxRetries caps extra attempts after a transient failure, to limit load on a struggling backend.
	defaultMaxRetries = 2

	// defaultEmptyBatchBackoff: consecutive empty /_batch/get responses before probing turns off; 0 disables it.
	defaultEmptyBatchBackoff = 24

	// retryBaseDelay / retryMaxDelay bound the exponential backoff between retries; full jitter on top (see sleepBackoff).
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
				logger.Warn("cacheprog: remote returned %d empty batches; "+
					"disabling further batch probes for this run (endpoint=%s)",
					b.emptyBatchBackoffThreshold, b.endpoint)
			})
		}
	}
}

// batchProbingOff reports whether the empty-batch backoff tripped, so cold keys miss without a round-trip.
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

// transientStatus reports whether a status is transient (5xx or 429); other 4xx, including 404, is definitive.
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

// doRetryGET issues an idempotent GET with the configured bounded-retry policy on transient failures.
func (b *WebBackend) doRetryGET(req *http.Request) (*http.Response, error) {
	return b.doRetry(req, b.maxRetries)
}

// doRetryGETN issues a GET with up to maxRetries retries; the index fetch caps this lower so it can't stall startup.
func (b *WebBackend) doRetryGETN(req *http.Request, maxRetries int) (*http.Response, error) {
	return b.doRetry(req, maxRetries)
}

// doRetryPUT issues an upload with doRetryGET's bounded-retry policy; PUT is idempotent here (key = content address).
// The body is []byte so each retry rebuilds a fresh reader via req.GetBody, or a 503 admission shed silently drops it.
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
		// Honor a server Retry-After (e.g. a 503 admission shed), but never sleep less than the jittered backoff.
		var retryAfter time.Duration
		if err == nil {
			retryAfter = parseRetryAfter(resp)
			// Drain and close so the connection returns to the pool.
			io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
			resp.Body.Close()
		}
		b.sleepBackoff(attempt, retryAfter)
	}
}

// sleepBackoff waits before a retry (full jitter, so parallel builds don't sync into a thundering herd), returning
// early on shutdown. A non-zero atLeast (a server Retry-After hint) raises the floor, still capped at retryMaxDelay.
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
