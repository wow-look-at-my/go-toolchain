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

	// retryBaseDelay / retryMaxDelay bound the exponential backoff between
	// retries. Full jitter is applied on top (see sleepBackoff).
	retryBaseDelay = 100 * time.Millisecond
	retryMaxDelay  = 2 * time.Second
)

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

// breakerFault reports whether an HTTP status should count toward the circuit
// breaker. It is intentionally NARROWER than transientStatus: a 503 is the
// cache server's admission-control backpressure (it sheds excess concurrent
// requests with 503 + Retry-After), which is healthy load-shedding, not a
// backend fault. Counting a burst of 503 sheds toward the breaker would
// wrongly disable the remote cache for the rest of the run (GETs short-circuit
// to clean misses) just because the build briefly pushed harder than the
// server's concurrency limit. So a 503 is still RETRIED (transientStatus stays
// true) and harmlessly resets the streak via noteRemoteResult, but it does not
// increment it; every other transient status (500/502/504/429) and every
// network error still counts as a genuine fault.
func breakerFault(code int) bool {
	return transientStatus(code) && code != http.StatusServiceUnavailable
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

// noteRemoteStatus records an HTTP response status against the circuit breaker.
// It applies breakerFault, so a 503 admission-shed (healthy backpressure) is
// never counted as a fault while 500/502/504/429 still are. A transport error
// with no response must call noteRemoteResult(true) directly.
func (b *WebBackend) noteRemoteStatus(code int) {
	b.noteRemoteResult(breakerFault(code))
}

// remoteDisabled reports whether the circuit breaker has tripped, meaning the
// remote backend is being skipped entirely for the rest of this process.
func (b *WebBackend) remoteDisabled() bool {
	return b.breakerTripped.Load()
}

// noteRemoteResult feeds the outcome of one logical remote operation to the
// circuit breaker. transientFailure is true for a genuine backend fault (a
// 500/502/504/429 or a timeout/network error — see breakerFault); false for a
// success, a definitive non-failure (e.g. a 404 miss), OR a 503 admission shed
// (healthy backpressure, retried but not counted). A non-failure resets the
// consecutive-failure streak; reaching the threshold trips the breaker to
// local-only for the rest of the run.
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

// doRetryGET issues an idempotent GET (index fetch, individual get, batch get)
// with bounded retries on transient failures, using exponential backoff with
// full jitter. It returns the final (resp, err) exactly as http.Client.Do
// would, so callers handle status codes and bodies unchanged; it never retries
// a definitive (<500, non-429) response. Circuit-breaker accounting is the
// caller's job (one note per logical op), so retries here are not double-counted.
func (b *WebBackend) doRetryGET(req *http.Request) (*http.Response, error) {
	return b.doRetry(req)
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
	return b.doRetry(req)
}

// doRetry is the shared retry loop behind doRetryGET and doRetryPUT. It retries
// a transient response (transientStatus) up to maxRetries times, sleeping
// max(exponential-jittered backoff, server Retry-After) capped at retryMaxDelay
// between attempts, and rewinds the request body from req.GetBody on each retry.
// It returns the final (resp, err) exactly as http.Client.Do would. Note a 503
// IS retried here (it is transient) even though it does NOT count toward the
// circuit breaker (see breakerFault) — backpressure should be backed off and
// retried, not treated as an outage.
func (b *WebBackend) doRetry(req *http.Request) (*http.Response, error) {
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
		if attempt >= b.maxRetries {
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
