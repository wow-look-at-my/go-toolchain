package cache

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// getIndividual fetches a single object stored under an individual cache key.
// parentCtx, when non-nil and carrying a valid span, becomes the parent
// of the emitted cacheprog.web.get span — used by sendBatch's 404/405
// fallback path so individual GETs nest under the batch span instead of
// detaching to the run-level root.
func (b *WebBackend) getIndividual(parentCtx context.Context, actionID, key string) (string, io.ReadCloser, int64, time.Time, bool, error) {
	_, span := b.tracer.StartFromCtx(parentCtx, "cacheprog.web.get",
		attribute.String("cacheprog.action_id", shortID(actionID)),
	)
	defer span.End()

	req, err := http.NewRequest("GET", b.url(key), nil)
	if err != nil {
		span.SetStatus(codes.Error, "build request")
		return "", nil, 0, time.Time{}, true, nil
	}
	b.signRequest(req)

	b.Pool.Acquire()
	httpStart := time.Now()
	resp, err := b.doRetryGET(req)
	if err != nil {
		b.Pool.Release()
		b.MissNetwork.Increment()
		markSpanErr(span, "network", err)
		markSpanMiss(span, "network")
		logger.Warn("cacheprog: web get %s: %v", shortID(actionID), err)
		return "", nil, 0, time.Time{}, true, nil
	}
	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))

	if resp.StatusCode == 404 {
		resp.Body.Close()
		b.Pool.Release()
		b.MissHTTP404.Increment()
		// Drop any stale index claim so the PUT path re-uploads the object;
		// without this the key 404s forever and is never re-published.
		b.reclaimAbsent(key)
		markSpanMiss(span, "http_404")
		return "", nil, 0, time.Time{}, true, nil
	}
	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		b.Pool.Release()
		b.MissHTTPError.Increment()
		markSpanMiss(span, fmt.Sprintf("http_%d", resp.StatusCode))
		b.errLog.Record("web get", resp.StatusCode, actionID, string(respBody))
		return "", nil, 0, time.Time{}, true, nil
	}

	// Native metadata header, with a fallback to the deprecated S3-style header
	// so a new client still reads the outputid from an older (not-yet-upgraded)
	// cache server that only emits X-Amz-Meta-*.
	outputID := resp.Header.Get("X-Cache-Meta-Outputid")
	if outputID == "" {
		outputID = resp.Header.Get("X-Amz-Meta-Outputid")
	}
	if outputID == "" {
		resp.Body.Close()
		b.Pool.Release()
		b.MissNoOutputID.Increment()
		markSpanMiss(span, "no_outputid")
		logger.Warn("cacheprog: web get %s: missing outputid metadata", shortID(actionID))
		return "", nil, 0, time.Time{}, true, nil
	}

	compressed, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	b.Pool.Release()
	if b.Latency != nil {
		b.Latency.HTTPGet.Record(time.Since(httpStart))
	}
	if err != nil {
		b.MissReadBody.Increment()
		markSpanErr(span, "read_body", err)
		markSpanMiss(span, "read_body")
		logger.Warn("cacheprog: web get %s: read body: %v", shortID(actionID), err)
		return "", nil, 0, time.Time{}, true, nil
	}

	decompressStart := time.Now()
	decompressed, err := decompressData(compressed)
	if b.Latency != nil {
		b.Latency.Decompress.Record(time.Since(decompressStart))
	}
	if err != nil {
		b.MissDecompress.Increment()
		markSpanErr(span, "decompress", err)
		markSpanMiss(span, "decompress")
		logger.Warn("cacheprog: web get %s: decompress: %v", shortID(actionID), err)
		return "", nil, 0, time.Time{}, true, nil
	}

	// End-to-end integrity check: the body must hash to its advertised
	// outputID (the go content hash). A mismatch means the remote object is
	// corrupt — truncated, badly decoded, or poisoned/rotted in the remote cache — and
	// serving it would feed the go command a damaged object (e.g. a module
	// index -> "corrupt index"). Refuse to serve, and drop the key from the
	// index so the next recompute re-uploads (overwrites) it clean instead of
	// skipping the Put as already-present.
	if got, ok := outputIDMatches(outputID, decompressed); !ok {
		b.MissChecksum.Increment()
		b.Stats.Corrupt.Increment()
		markSpanMiss(span, "checksum")
		b.removeClaimed(key)
		logger.Warn("cacheprog: web get %s: body checksum mismatch (want outputid=%s, got sha256=%s, len=%d); evicting and treating as miss",
			shortID(actionID), shortID(outputID), shortID(got), len(decompressed))
		return "", nil, 0, time.Time{}, true, nil
	}

	// Cross-contamination guard: a compiled package self-certifies its action
	// key in its build id. A body that hashes to its outputID but whose build id
	// belongs to a DIFFERENT action is a poisoned mapping (the wrong object under
	// this key) that the hash check above cannot catch -- e.g. internal/reflectlite
	// export data served for the `runtime` action, surfacing as "imported as
	// reflectlite". Refuse it, and evict the key so a recompute re-uploads
	// (overwrites) the correct object.
	if act, ok := buildIDMatchesAction(actionID, decompressed); !ok {
		b.MissBuildID.Increment()
		b.Stats.Corrupt.Increment()
		markSpanMiss(span, "buildid_mismatch")
		b.removeClaimed(key)
		logger.Warn("cacheprog: web get %s: build-id action mismatch (want action=%s, got action=%s, len=%d); evicting and treating as miss",
			shortID(actionID), expectedBuildIDAction(actionID), act, len(decompressed))
		return "", nil, 0, time.Time{}, true, nil
	}

	// Module-index guard: a Go module index blob carries no build id and does not
	// self-certify which directory it indexes, so neither the outputID hash nor
	// the build-id check can prove it belongs under this key (see isGoModuleIndex).
	// A wrong one is silently fatal at package load ("package runtime is not in
	// std" / "corrupt index"), so refuse it from the shared cache and let cmd/go
	// recompute it locally (a cheap directory read). Evict the claim so the
	// recompute is free to re-Put.
	if isGoModuleIndex(decompressed) {
		b.MissModuleIndex.Increment()
		markSpanMiss(span, "module_index")
		b.removeClaimed(key)
		logger.Warn("cacheprog: web get %s: refusing module-index blob (unverifiable under this key, len=%d); treating as miss",
			shortID(actionID), len(decompressed))
		return "", nil, 0, time.Time{}, true, nil
	}

	t := time.Now()
	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		if parsed, parseErr := time.Parse(http.TimeFormat, lm); parseErr == nil {
			t = parsed
		}
	}

	b.Stats.Hits.Increment()
	span.SetAttributes(attribute.Bool("cacheprog.hit", true),
		attribute.Int("cacheprog.bytes_compressed", len(compressed)),
		attribute.Int("cacheprog.bytes_uncompressed", len(decompressed)),
		attribute.String("cacheprog.label", describeData(decompressed)))
	return outputID, io.NopCloser(bytes.NewReader(decompressed)), int64(len(decompressed)), t, false, nil
}

// getBatch enqueues this key on the coalescer and waits for the result.
// Multiple concurrent callers funnel into the same outgoing HTTP request
// instead of each making their own — see batchCoalescer / sendBatch.
func (b *WebBackend) getBatch(actionID, key string) (string, io.ReadCloser, int64, time.Time, bool, error) {
	respCh := make(chan batchResp, 1)
	select {
	case b.batchReqCh <- batchReq{actionID: actionID, key: key, resp: respCh}:
	case <-b.batchStop:
		// Backend is closing — return miss so the caller can fall back.
		return "", nil, 0, time.Time{}, true, nil
	}
	select {
	case r := <-respCh:
		return r.outputID, r.body, r.size, r.t, r.miss, nil
	case <-b.batchDone:
		// Shutdown raced our enqueue: the coalescer exited and may never
		// drain batchReqCh. If it DID process our request, the reply is
		// already buffered in respCh (batchDone closes only after all
		// sendBatch goroutines finish); otherwise degrade to a clean miss
		// instead of blocking forever on a reply that will never come.
		select {
		case r := <-respCh:
			return r.outputID, r.body, r.size, r.t, r.miss, nil
		default:
			return "", nil, 0, time.Time{}, true, nil
		}
	}
}

// removeClaimed removes a key that was optimistically added to the index
// when the upload fails, so it can be retried on the next attempt.
func (b *WebBackend) removeClaimed(key string) {
	b.keysMu.Lock()
	b.keys.Remove(key)
	b.keysMu.Unlock()
}
