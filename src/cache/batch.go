package cache

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// batchGetRequest is the JSON body sent to the server's /_batch/get endpoint.
type batchGetRequest struct {
	Keys     []string `json:"keys"`
	Prefetch bool     `json:"prefetch"`
}

// batchGetManifest is the manifest entry in the server's tar response.
type batchGetManifest struct {
	Entries []batchGetManifestEntry `json:"entries"`
}

type batchGetManifestEntry struct {
	Key      string            `json:"key"`
	Size     int64             `json:"size"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Prefetch bool              `json:"prefetch,omitempty"`
}

// BatchEntry holds a single cache entry from a batch GET response.
type BatchEntry struct {
	Key      string
	OutputID string
	Data     []byte
	Prefetch bool
}

// parseBatchResponse reads a tar stream from the server's /_batch/get
// endpoint and returns all entries with their data and metadata.
func parseBatchResponse(r io.Reader) ([]BatchEntry, error) {
	tr := tar.NewReader(r)

	var manifest batchGetManifest
	dataByKey := map[string][]byte{}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}

		raw, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read entry %s: %w", hdr.Name, err)
		}

		if hdr.Name == "manifest.json" {
			if err := json.Unmarshal(raw, &manifest); err != nil {
				return nil, fmt.Errorf("parse manifest: %w", err)
			}
			continue
		}

		if len(hdr.Name) > 5 && hdr.Name[:5] == "data/" {
			dataByKey[hdr.Name[5:]] = raw
		}
	}

	var entries []BatchEntry
	for _, me := range manifest.Entries {
		data, ok := dataByKey[me.Key]
		if !ok {
			continue
		}
		entries = append(entries, BatchEntry{
			Key:      me.Key,
			OutputID: me.Metadata["outputid"],
			Data:     data,
			Prefetch: me.Prefetch,
		})
	}
	return entries, nil
}

// batchCoalescer collects incoming batchReqs on a short coalescing window
// and dispatches each batch as a single HTTP request to the server's batch
// endpoint. Up to batchMaxKeys keys per HTTP request.
func (b *WebBackend) batchCoalescer() {
	defer close(b.batchDone)

	var pending []batchReq
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}

	flush := func() {
		if len(pending) == 0 {
			return
		}
		batch := pending
		pending = nil
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		b.batchHTTPWG.Add(1)
		go func() {
			defer b.batchHTTPWG.Done()
			b.sendBatch(batch)
		}()
	}

	for {
		select {
		case req, ok := <-b.batchReqCh:
			if !ok {
				flush()
				b.batchHTTPWG.Wait()
				return
			}
			if len(pending) == 0 {
				timer.Reset(batchCoalesceWait)
			}
			pending = append(pending, req)
			if len(pending) >= batchMaxKeys {
				flush()
			}
		case <-timer.C:
			flush()
		case <-b.batchStop:
			flush()
			b.batchHTTPWG.Wait()
			return
		}
	}
}

// sendBatch issues a single HTTP request to /_batch/get for all keys in reqs,
// distributes the matching entries back to the waiting callers via their
// reply channels, and feeds prefetched entries to OnBatchEntries.
func (b *WebBackend) sendBatch(reqs []batchReq) {
	start := time.Now()
	keys := make([]string, len(reqs))
	for i, r := range reqs {
		keys[i] = r.key
	}

	ctx, span := b.tracer.Start("cacheprog.web.batch_get",
		attribute.Int("cacheprog.batch.requests", len(reqs)))
	defer span.End()

	// Transient failure only; never marks knownMiss, since only an
	// authoritative OK-without-key response proves absence.
	respondAllMiss := func(reason *AtomicCounter) {
		for _, r := range reqs {
			if reason != nil && b.keyKnown(r.key) {
				reason.Increment()
			}
			r.resp <- batchResp{miss: true}
		}
	}

	reqBody, _ := json.Marshal(batchGetRequest{Keys: keys, Prefetch: true})
	batchURL := b.endpoint + "/" + b.bucket + "/_batch/get"
	// POST, not GET-with-body, since a body-carrying GET is proxy-hostile.
	httpReq, err := http.NewRequest("POST", batchURL, bytes.NewReader(reqBody))
	if err != nil {
		span.SetStatus(codes.Error, "build request")
		respondAllMiss(nil)
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	b.signRequest(httpReq)

	b.Pool.Acquire()
	resp, err := b.doRetryGET(httpReq)
	if err != nil {
		b.Pool.Release()
		markSpanErr(span, "network", err)
		logger.Warn("cacheprog: web batch get: %v", err)
		respondAllMiss(&b.MissNetwork)
		return
	}
	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))

	if resp.StatusCode != 200 {
		resp.Body.Close()
		b.Pool.Release()
		// Not-found or method-not-allowed → server has no batch endpoint; fall back to individual
		// GETs for every caller in this batch.
		if resp.StatusCode == 404 || resp.StatusCode == 405 {
			span.SetAttributes(attribute.Bool("cacheprog.batch.fallback_individual", true))
			for _, r := range reqs {
				outputID, body, size, t, miss, _ := b.getIndividual(ctx, r.actionID, r.key)
				r.resp <- batchResp{outputID: outputID, body: body, size: size, t: t, miss: miss}
			}
			return
		}
		// 5xx etc. — coalesced via errLog; its group total covers the request count.
		span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d", resp.StatusCode))
		for _, r := range reqs {
			b.errLog.Record("web batch get", resp.StatusCode, r.actionID, "")
		}
		respondAllMiss(&b.MissHTTPError)
		return
	}

	entries, err := parseBatchResponse(resp.Body)
	resp.Body.Close()
	b.Pool.Release()
	if err != nil {
		markSpanErr(span, "parse response", err)
		logger.Warn("cacheprog: web batch get: parse: %v", err)
		respondAllMiss(&b.MissReadBody)
		return
	}

	// A run of empty batches stops probing after a threshold; any non-empty batch resets it.
	b.noteBatchEntries(len(entries))

	var nPrefetch int
	for _, e := range entries {
		if e.Prefetch {
			nPrefetch++
		}
	}
	span.SetAttributes(attribute.Int("cacheprog.batch.entries_returned", len(entries)),
		attribute.Int("cacheprog.batch.prefetched", nPrefetch))
	b.errLog.RecordBatchHTTP(len(reqs), len(entries), nPrefetch, time.Since(start))

	// Index returned entries by key for constant-time lookup.
	entryByKey := make(map[string]*BatchEntry, len(entries))
	for i := range entries {
		entryByKey[entries[i].Key] = &entries[i]
	}

	// Distribute responses to the waiting compiler goroutines AHEAD of anything else; prefetch
	// ingestion is housekeeping and runs asynchronously below. (It used to run
	// inline before the reply loop, so every caller blocked on decompress +
	// hash + pack-append work for entries nobody was waiting on.)
	//
	// Each request gets its own cacheprog.web.get child span so the batch
	// span has a child per individual cache request it served — making the
	// parent/child hierarchy in OTel match the build's logical structure.
	for _, r := range reqs {
		_, itemSpan := b.tracer.StartFromCtx(ctx, "cacheprog.web.get",
			attribute.String("cacheprog.action_id", shortID(r.actionID)))
		e, ok := entryByKey[r.key]
		if !ok {
			// Authoritative absence: a healthy response omitted this key. Drop the
			// stale index claim (reclaimAbsent) so the PUT path re-uploads it.
			if b.reclaimAbsent(r.key) {
				b.MissHTTP404.Increment()
			}
			markSpanMiss(itemSpan, "not_in_batch_response")
			itemSpan.End()
			r.resp <- batchResp{miss: true}
			continue
		}
		// Missing outputid is a metadata gap, not a corrupt body — count it as
		// no-outputid (mirroring getIndividual) rather than a misleading checksum mismatch below.
		if e.OutputID == "" {
			b.MissNoOutputID.Increment()
			markSpanMiss(itemSpan, "no_outputid")
			itemSpan.End()
			logger.Warn("cacheprog: web batch get %s: missing outputid metadata", shortID(r.actionID))
			r.resp <- batchResp{miss: true}
			continue
		}
		decompressed, err := decompressData(e.Data)
		if err != nil {
			markSpanErr(itemSpan, "decompress", err)
			markSpanMiss(itemSpan, "decompress")
			itemSpan.End()
			logger.Warn("cacheprog: web batch get %s: decompress: %v", shortID(r.actionID), err)
			r.resp <- batchResp{miss: true}
			continue
		}
		// End-to-end integrity check (see outputIDMatches): refuse to serve a
		// body that does not hash to its advertised outputID. A corrupt remote
		// object must never reach the go command as a "valid" cache hit. The
		// key is absent from the in-memory index (that is why it took the batch
		// path), so a subsequent recompute+Put re-uploads it clean on its own.
		if got, ok := outputIDMatches(e.OutputID, decompressed); !ok {
			b.MissChecksum.Increment()
			b.Stats.Corrupt.Increment()
			markSpanMiss(itemSpan, "checksum")
			itemSpan.End()
			logger.Warn("cacheprog: web batch get %s: body checksum mismatch (want outputid=%s, got sha256=%s, len=%d); treating as miss",
				shortID(r.actionID), shortID(e.OutputID), shortID(got), len(decompressed))
			r.resp <- batchResp{miss: true}
			continue
		}
		// Cross-contamination guard (see buildIDMatchesAction): refuse a compiled
		// object whose build id belongs to a different action than requested. The
		// hash check above only proves body<->outputID consistency, not that the
		// object belongs under this action key.
		if act, ok := buildIDMatchesAction(r.actionID, decompressed); !ok {
			b.MissBuildID.Increment()
			b.Stats.Corrupt.Increment()
			markSpanMiss(itemSpan, "buildid_mismatch")
			itemSpan.End()
			logger.Warn("cacheprog: web batch get %s: build-id action mismatch (want action=%s, got action=%s, len=%d); treating as miss",
				shortID(r.actionID), expectedBuildIDAction(r.actionID), act, len(decompressed))
			r.resp <- batchResp{miss: true}
			continue
		}
		// Module-index guard (see isGoModuleIndex): an index blob cannot be proven
		// to belong under this key, and the wrong index is fatal at package load.
		// Refuse it and let cmd/go recompute the index locally.
		if isGoModuleIndex(decompressed) {
			b.MissModuleIndex.Increment()
			markSpanMiss(itemSpan, "module_index")
			itemSpan.End()
			logger.Warn("cacheprog: web batch get %s: refusing module-index blob (unverifiable under this key, len=%d); treating as miss",
				shortID(r.actionID), len(decompressed))
			r.resp <- batchResp{miss: true}
			continue
		}
		b.Stats.Hits.Increment()
		itemSpan.SetAttributes(
			attribute.Bool("cacheprog.hit", true),
			attribute.Int("cacheprog.bytes_compressed", len(e.Data)),
			attribute.Int("cacheprog.bytes_uncompressed", len(decompressed)),
		)
		itemSpan.End()
		r.resp <- batchResp{
			outputID: e.OutputID,
			body:     io.NopCloser(bytes.NewReader(decompressed)),
			size:     int64(len(decompressed)),
			t:        time.Now(),
		}
	}

	// Hand only NON-requested (prefetch) entries to the populator, async so no
	// caller waits: a requested entry is already verified and written by
	// handleGet, so feeding it here too would double the verify work. The
	// goroutine joins batchHTTPWG, so shutdown still waits for ingestion.
	if b.OnBatchEntries != nil {
		requested := set.New[string](len(reqs))
		for _, r := range reqs {
			requested.Add(r.key)
		}
		var extra []BatchEntry
		for _, e := range entries {
			if !requested.Contains(e.Key) {
				extra = append(extra, e)
			}
		}
		if len(extra) > 0 {
			b.batchHTTPWG.Add(1)
			go func() {
				defer b.batchHTTPWG.Done()
				b.OnBatchEntries(extra)
			}()
		}
	}
}
