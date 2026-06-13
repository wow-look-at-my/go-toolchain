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

// doRetryGET issues an idempotent GET (index fetch, individual get, batch get)
// with bounded retries on transient failures, using exponential backoff with
// full jitter. It returns the final (resp, err) exactly as http.Client.Do
// would, so callers handle status codes and bodies unchanged; it never retries
// a definitive (<500, non-429) response. Circuit-breaker accounting is the
// caller's job (one note per logical op), so retries here are not double-counted.
func (b *WebBackend) doRetryGET(req *http.Request) (*http.Response, error) {
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
		if attempt >= b.maxRetries {
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
