package cache

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
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

	// Atomically check-and-claim: if the key is already known (or being
	// uploaded by another goroutine), skip immediately.
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

	// Release the optimistic claim unless the object is successfully queued for
	// upload (enqueued onto the coalescer or, in fallback mode, dispatched on the
	// single-PUT path). Each path sets queued=true once it has taken ownership of
	// the claim; the coalescer/single-PUT path is then responsible for any later
	// rollback (a per-object server error or a whole-batch final failure).
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

	// Write-side cross-contamination guard: never publish a compiled package to
	// the shared cache under a key that disagrees with the package's own build
	// id. The body<->outputID hash is self-consistent for a mis-keyed object, so
	// only this check stops a swapped (actionID, object) pair from poisoning the
	// remote cache for every other consumer. The deferred removeClaimed releases
	// the optimistic index claim, so a later correct Put for this key still runs.
	if act, ok := buildIDMatchesAction(actionID, raw); !ok {
		b.PutRefusedBuildID.Increment()
		markSpanMiss(span, "buildid_mismatch")
		fmt.Fprintf(os.Stderr, "cacheprog: web put %s: refusing upload, build-id action mismatch (want action=%s, got action=%s); object does not belong under this key\n",
			shortID(actionID), expectedBuildIDAction(actionID), act)
		return nil
	}

	// Never publish a Go module index to the shared cache. It cannot be verified
	// against an action key on the way back in (see isGoModuleIndex), so a
	// consumer has no way to tell a correct one from a mis-keyed (build-breaking)
	// one -- the read side refuses all of them. Uploading is therefore pure
	// downside: wasted bytes plus a standing poison vector for any client. Every
	// consumer recomputes the index locally, so dropping the upload costs nothing.
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

	// Assemble the manifest metadata map: lowercased meta names WITHOUT the
	// X-Cache-Meta- prefix, the SAME values a single PUT sends as headers. The
	// single-PUT fallback re-derives these as headers from this map (see
	// metadataHeaders), so this map is the single source of truth for both paths.
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

	// Server has no batch endpoint (learned from an earlier 404/405): fall back
	// to the per-object single-PUT path. This keeps the client working against an
	// un-upgraded server; the #272 retry is the floor.
	if b.batchPutUnsupported.Load() {
		queued = true // putSingle owns the claim from here on.
		return b.putSingle(pr)
	}

	// Enqueue onto the coalescer and return immediately. The coalescer owns the
	// claim from here: it keeps it on stored/conflict/dropped and rolls it back
	// on a per-object error or a whole-batch final failure.
	select {
	case b.putBatchReqCh <- pr:
		queued = true
	case <-b.putBatchStop:
		// Backend is closing — drop the claim so a later run re-uploads.
	}
	return nil
}

// metadataHeaders renders a manifest metadata map back into the X-Cache-Meta-*
// request headers a single PUT uses. The map keys are the lowercased meta names
// without the prefix; this is the inverse of the map assembled in Put, so the
// single-PUT fallback sends byte-identical metadata to what the batch manifest
// carries. Header keys are canonicalized by net/http on Set.
func metadataHeaders(meta map[string]string) http.Header {
	h := http.Header{}
	for name, val := range meta {
		h.Set("X-Cache-Meta-"+name, val)
	}
	return h
}

// srcMetaMaxFiles / srcMetaMaxBytes bound the X-Cache-Meta-Src metadata value.
// The uncapped list (every .go basename compiled into the archive) could run to
// kilobytes for large packages and blow the cache server's ~4 KiB shared ext4
// xattr block — the server now degrades gracefully by dropping the oversized
// key, but then the provenance data is simply lost. A capped list keeps the
// most useful prefix and always fits.
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
