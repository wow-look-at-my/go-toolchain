package cache

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// putReq is one prepped object queued for the PUT coalescer. The per-object
// preparation (optimistic index claim, build-id/module-index guard, lz4) has
// already run in Put; the coalescer only frames and ships these.
type putReq struct {
	actionID   string
	key        string
	outputID   string
	raw        []byte            // uncompressed body, kept for the single-PUT fallback / label
	compressed []byte            // lz4-compressed body, the data/<key> member bytes
	metadata   map[string]string // manifest metadata: lowercased meta names sans X-Cache-Meta-
}

// Put stores a cached object with LZ4 compression. The per-object preparation
// (optimistic index claim, read, build-id/module-index write guards, lz4, and
// the metadata map) runs synchronously here; the upload itself is COALESCED:
// the prepped object is enqueued onto the PUT coalescer (batchPutCoalescer),
// which ships many objects as one /_batch/put tar instead of one HTTP PUT per
// object — a CI build stores thousands of objects and the per-object PUT storm
// saturated the cache server's admission control. Put returns nil immediately
// (fire-and-forget, matching the prior async model); the coalescer reports
// per-object outcomes (rolling back a claim on a server-side error) and the
// whole batch is retried as one tar on a 503 shed. If the server does not
// support the batch endpoint (sticky batchPutUnsupported, set on a 404/405),
// Put falls back to the per-object doRetryPUT path — the #272 single-PUT retry
// is the floor.
func (b *WebBackend) Put(actionID, outputID string, body io.Reader, bodySize int64) error {
	key := b.key(actionID)

	// Atomically check-and-claim: skip if the key is already known or being uploaded.
	b.keysMu.Lock()
	if b.keys.Contains(key) {
		b.keysMu.Unlock()
		b.PutSkippedKnown.Increment()
		return nil
	}
	b.keys.Add(key)
	b.keysMu.Unlock()

	_, span := b.tracer.Start("cacheprog.web.put",
		attribute.String("cacheprog.action_id", shortID(actionID)),
		attribute.Int64("cacheprog.bytes_uncompressed", bodySize))
	defer span.End()

	// Release the claim unless queued=true: a path sets that once it owns rollback.
	var queued bool
	defer func() {
		if !queued {
			b.removeClaimed(key)
		}
	}()

	raw, err := io.ReadAll(body)
	if err != nil {
		markSpanErr(span, "read body", err)
		return fmt.Errorf("web put read: %w", err)
	}

	// Cross-contamination guard: refuse to publish a package under a key that disagrees
	// with its own build id. The body<->outputID hash alone cannot catch a swapped
	// (actionID, object) pair, so this is the only defense against poisoning the cache.
	if act, ok := buildIDMatchesAction(actionID, raw); !ok {
		b.PutRefusedBuildID.Increment()
		markSpanMiss(span, "buildid_mismatch")
		logger.Warn("cacheprog: web put %s: refusing upload, build-id action mismatch (want action=%s, got action=%s); object does not belong under this key",
			shortID(actionID), expectedBuildIDAction(actionID), act)
		return nil
	}

	// Never publish a Go module index: the read side can't verify it, so it refuses every
	// upload; recomputing locally is free.
	if isGoModuleIndex(raw) {
		b.PutRefusedModIndex.Increment()
		markSpanMiss(span, "module_index")
		return nil
	}

	compressStart := time.Now()
	compressed, err := compressData(raw)
	if b.Latency != nil {
		b.Latency.Compress.Record(time.Since(compressStart))
	}
	if err != nil {
		markSpanErr(span, "compress", err)
		return fmt.Errorf("web put compress: %w", err)
	}
	span.SetAttributes(attribute.Int("cacheprog.bytes_compressed", len(compressed)),
		attribute.String("cacheprog.label", describeData(raw)))

	// meta holds lowercased names without the X-Cache-Meta- prefix; metadataHeaders
	// derives the single-PUT headers from this same map, keeping both paths in sync.
	meta := map[string]string{
		"outputid":    outputID,
		"object-type": detectObjectType(raw),
		"body-size":   strconv.FormatInt(bodySize, 10),
		"compression": "lz4",
		"created":     time.Now().UTC().Format(time.RFC3339),
	}
	if b.version != "" {
		meta["toolchain-version"] = b.version
	}
	if b.module != "" {
		meta["module"] = b.module
	}
	if goVer, target := parseArchiveHeader(raw); goVer != "" {
		meta["go-version"] = goVer
		meta["target"] = target
	}
	if pkg := parseImportPath(raw); pkg != "" {
		meta["pkg"] = pkg
	}
	if files := parseSourceFiles(raw); len(files) > 0 {
		meta["src"] = capSrcList(files)
	}

	pr := putReq{
		actionID:   actionID,
		key:        key,
		outputID:   outputID,
		raw:        raw,
		compressed: compressed,
		metadata:   meta,
	}

	// No batch endpoint (learned from an earlier 404/405): fall back to the single-PUT path.
	if b.batchPutUnsupported.Load() {
		queued = true // putSingle owns the claim from here on.
		return b.putSingle(pr)
	}

	// Enqueue onto the coalescer, which now owns the claim, until a per-object or whole-batch failure rolls it back.
	select {
	case b.putBatchReqCh <- pr:
		queued = true
	case <-b.putBatchStop:
		// Backend is closing — drop the claim so a later run re-uploads.
	}
	return nil
}

// metadataHeaders renders the meta map back into X-Cache-Meta-* headers, the inverse of
// the map built in Put.
func metadataHeaders(meta map[string]string) http.Header {
	h := http.Header{}
	for name, val := range meta {
		h.Set("X-Cache-Meta-"+name, val)
	}
	return h
}

// srcMetaMaxFiles/srcMetaMaxBytes bound the Src metadata value so it always fits the
// cache server's ~4 KiB shared ext4 xattr block.
const (
	srcMetaMaxFiles = 8
	srcMetaMaxBytes = 256
)

// capSrcList renders a source-file basename list as the Src metadata value,
// bounded to at most srcMetaMaxFiles names and srcMetaMaxBytes bytes in total;
// names past the cap are summarized as a trailing "+N more".
func capSrcList(files []string) string {
	total := len(files)
	if len(files) > srcMetaMaxFiles {
		files = files[:srcMetaMaxFiles]
	}
	for {
		s := strings.Join(files, " ")
		if dropped := total - len(files); dropped > 0 {
			suffix := "+" + strconv.Itoa(dropped) + " more"
			if s == "" {
				s = suffix
			} else {
				s += " " + suffix
			}
		}
		if len(s) <= srcMetaMaxBytes || len(files) == 0 {
			return s
		}
		files = files[:len(files)-1]
	}
}
