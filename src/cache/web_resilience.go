package cache

import (
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
	// defaultMaxRetries bounds how many extra attempts a transient GET-class
	// failure gets. Small on purpose — retries against a struggling backend add
	// to the very load that is hurting it, so the circuit breaker (below) is the
	// real protection; retries only paper over isolated single-request blips.
	defaultMaxRetries = 2

	// defaultBreakerThreshold is the number of consecutive remote failures that
	// trips the circuit breaker to local-only mode for the rest of the process.
	// Chosen well above the handful of ops any unit test issues (so tests are
	// unaffected) yet far below the thousands a real build makes — so a genuine
	// outage stops hammering the backend after a few dozen wasted requests, not
	// thousands.
	defaultBreakerThreshold = 24

	// defaultEmptyBatchBackoff is the number of consecutive zero-entry /_batch/get
	// responses that disables further batch probing for the rest of the process.
	// Distinct from the circuit breaker: an empty-but-200 batch is a HEALTHY
	// backend that simply has none of this build's keys, so the breaker never
	// trips for it. CI batches were ~1.6 keys / ~45ms each, so backing off after
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
// This is deliberately separate from noteRemoteResult / the circuit breaker: an
// empty-but-200 response is not a backend failure and must not affect breaker
// semantics.
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
// backend problem worth retrying and counting toward the circuit breaker. 5xx
// (gateway/upstream trouble — the 502 storm) and 429 (overload) are transient;
// every 4xx, including a 404 cache miss, is a definitive answer from a healthy
// backend and must NOT trip the breaker.
func transientStatus(code int) bool {
	return code >= 500 || code == http.StatusTooManyRequests
}

// remoteDisabled reports whether the circuit breaker has tripped, meaning the
// remote backend is being skipped entirely for the rest of this process.
func (b *WebBackend) remoteDisabled() bool {
	return b.breakerTripped.Load()
}

// noteRemoteResult feeds the outcome of one logical remote operation to the
// circuit breaker. transientFailure is true for a 5xx/429/timeout/network
// error; false for a success or any definitive non-failure (e.g. a 404 miss).
// A non-failure resets the consecutive-failure streak; reaching the threshold
// trips the breaker to local-only for the rest of the run.
func (b *WebBackend) noteRemoteResult(transientFailure bool) {
	if b.breakerThreshold <= 0 || b.breakerTripped.Load() {
		return
	}
	if !transientFailure {
		b.breakerFailures.Store(0)
		return
	}
	if b.breakerFailures.Add(1) >= int64(b.breakerThreshold) {
		b.tripBreaker()
	}
}

// tripBreaker switches the backend to local-only mode and logs a single clear
// warning. Once tripped it stays tripped for the lifetime of the process — a
// build either has a healthy cache or it does not; flapping mid-build would just
// reintroduce the load problem the breaker exists to prevent.
func (b *WebBackend) tripBreaker() {
	if b.breakerTripped.CompareAndSwap(false, true) {
		b.breakerLogOnce.Do(func() {
			w := b.breakerLog
			if w == nil {
				w = os.Stderr
			}
			fmt.Fprintf(w, "cacheprog: remote cache unhealthy after %d consecutive failures; "+
				"falling back to local-only for the rest of this run (endpoint=%s)\n",
				b.breakerThreshold, b.endpoint)
		})
	}
}

// doRetryGET issues an idempotent GET (individual get, batch get) with the
// configured number of bounded retries on transient failures. The index fetch
// uses doRetryGETN directly with its own tighter budget.
func (b *WebBackend) doRetryGET(req *http.Request) (*http.Response, error) {
	return b.doRetryGETN(req, b.maxRetries)
}

// doRetryGETN issues an idempotent GET with up to maxRetries retries on
// transient failures, using exponential backoff with full jitter. It returns
// the final (resp, err) exactly as http.Client.Do would, so callers handle
// status codes and bodies unchanged; it never retries a definitive (<500,
// non-429) response. Circuit-breaker accounting is the caller's job (one note
// per logical op), so retries here are not double-counted.
func (b *WebBackend) doRetryGETN(req *http.Request, maxRetries int) (*http.Response, error) {
	var (
		resp *http.Response
		err  error
	)
	for attempt := 0; ; attempt++ {
		// Rewind the body for a retry (batch get carries a small JSON body).
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
		// Drain and close a transient response before retrying so the
		// connection returns to the pool.
		if err == nil {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
			resp.Body.Close()
		}
		b.sleepBackoff(attempt)
	}
}

// sleepBackoff waits an exponentially increasing, fully jittered delay before a
// retry, returning early if the backend is shutting down. Full jitter (a random
// duration in [0, cap]) spreads concurrent retries so a parallel build does not
// synchronize into a thundering herd against a recovering backend.
func (b *WebBackend) sleepBackoff(attempt int) {
	d := retryBaseDelay << attempt
	if d > retryMaxDelay || d <= 0 {
		d = retryMaxDelay
	}
	d = time.Duration(rand.Int64N(int64(d) + 1))
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-b.batchStop:
	}
}
