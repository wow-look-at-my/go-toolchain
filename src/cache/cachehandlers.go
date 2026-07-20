package cache

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

func (s *Server) handleGet(req Request) Response {
	start := time.Now()
	actionID := s.actionKey(req.ActionID)
	mu := s.lock(actionID)

	lockStart := time.Now()
	mu.Lock()
	s.Latency.LockWait.Record(time.Since(lockStart))
	defer mu.Unlock()

	// Check local cache first.
	localStart := time.Now()
	meta, miss := s.local.Get(actionID)
	s.Latency.LocalGet.Record(time.Since(localStart))

	cacheLog := logger.WithSubsystem("cache")
	if !miss {
		s.sendStat(withAction(StatEvent{LocalHit: 1}, req.ActionID, "get", "hit-local", meta.Size, time.Since(start)))
		cacheLog.Debug("HIT local  %s output=%s size=%d", actionID, shortID(meta.OutputID), meta.Size)
		t := meta.Time
		return Response{
			ID:       req.ID,
			OutputID: hexToBytes(meta.OutputID),
			DiskPath: meta.DiskPath,
			Size:     meta.Size,
			Time:     &t,
		}
	}

	// Try remote backend.
	if s.remote == nil {
		s.Misses.Increment()
		s.sendStat(withAction(StatEvent{Miss: 1}, req.ActionID, "get", "miss", 0, time.Since(start)))
		cacheLog.Debug("MISS       %s", actionID)
		return Response{ID: req.ID, Miss: true}
	}

	remoteStart := time.Now()
	outputID, body, size, t, remoteMiss, err := s.remote.Get(actionID)

	if err != nil || remoteMiss {
		s.Misses.Increment()
		s.sendStat(withAction(StatEvent{Miss: 1}, req.ActionID, "get", "miss", 0, time.Since(start)))
		if err != nil {
			cacheLog.Debug("MISS       %s (remote error: %v)", actionID, err)
		} else {
			cacheLog.Debug("MISS       %s", actionID)
		}
		return Response{ID: req.ID, Miss: true}
	}
	s.Latency.RemoteGet.Record(time.Since(remoteStart))
	s.sendStat(withAction(StatEvent{RemoteHit: 1}, req.ActionID, "get", "hit-remote", size, time.Since(start)))
	cacheLog.Debug("HIT remote %s", actionID)
	defer body.Close()

	// Write to local cache for future hits. A remote body reaching this Put
	// has already passed the web ingestion guards (sha256 vs outputID,
	// build-id action, module-index refusal — web.go / batch.go): that is
	// load-bearing, because the local tier SERVES module indexes on the trust
	// that only local cmd/go ever stores one (see verify.go). A web-originated
	// index must never be materialized here.
	localPutStart := time.Now()
	diskPath, err := s.local.Put(actionID, outputID, body)
	s.Latency.LocalPut.Record(time.Since(localPutStart))

	if err != nil {
		return Response{ID: req.ID, Miss: true}
	}

	cacheLog.Debug("HIT remote %s [%s] output=%s", actionID, describeFile(diskPath), shortID(outputID))

	return Response{
		ID:       req.ID,
		OutputID: hexToBytes(outputID),
		DiskPath: diskPath,
		Size:     fileSize(diskPath),
		Time:     &t,
	}
}

func (s *Server) handlePut(req Request) Response {
	start := time.Now()
	actionID := s.actionKey(req.ActionID)
	outputID := fmt.Sprintf("%x", req.OutputID)
	mu := s.lock(actionID)

	lockStart := time.Now()
	mu.Lock()
	s.Latency.LockWait.Record(time.Since(lockStart))
	defer mu.Unlock()

	// Dedup check: already cached locally? Peek, not Get — serving the entry
	// the caller just recomputed is not a cache hit, and counting it inflated
	// the hit rate on warm rebuilds.
	localStart := time.Now()
	meta, miss := s.local.Peek(actionID)
	s.Latency.LocalGet.Record(time.Since(localStart))

	cacheLog := logger.WithSubsystem("cache")
	// Dedup ONLY when the stored entry is the same content cmd/go just
	// computed. A stored outputID that differs from the incoming PUT's must be
	// overwritten, not returned: cmd/go is the source of truth for its own
	// action keys (a legitimate re-Put after a nondeterministic rebuild, or a
	// mis-keyed body that slipped in — e.g. a web-prefetched object under a
	// module-index key). The old unconditional dedup made such an entry sticky
	// forever AND silently discarded the fresh correct body.
	if !miss && meta.OutputID == outputID {
		cacheLog.Debug("PUT  dedup  %s output=%s", actionID, shortID(meta.OutputID))
		return Response{ID: req.ID, DiskPath: meta.DiskPath, Size: meta.Size}
	}
	if !miss {
		cacheLog.Debug("PUT  replace %s stored-output=%s incoming-output=%s (stored entry does not match; overwriting)",
			actionID, shortID(meta.OutputID), shortID(outputID))
		// The remote claim for this key is as stale as the local entry was:
		// drop it so the remote Put below re-uploads the fresh body instead
		// of skipping it as already-present (see staleKeyForgetter).
		if f, ok := s.remote.(staleKeyForgetter); ok {
			f.ForgetStale(actionID)
		}
	}

	body := bytes.NewReader(req.Body)

	localPutStart := time.Now()
	diskPath, err := s.local.Put(actionID, outputID, body)
	s.Latency.LocalPut.Record(time.Since(localPutStart))

	if err != nil {
		return Response{ID: req.ID, Err: err.Error()}
	}
	s.sendStat(withAction(StatEvent{LocalPut: 1}, req.ActionID, "put", "put", int64(len(req.Body)), time.Since(start)))
	cacheLog.Debug("PUT  new    %s [%s] size=%d", actionID, describeData(req.Body), len(req.Body))
	// Async write to remote. The semaphore bounds concurrency to avoid
	// connection churn — each goroutine reuses a pooled HTTP connection
	// instead of creating (and discarding) a new TCP+TLS connection.
	//
	// req.Body is handed to the goroutine WITHOUT copying: it is a
	// per-request buffer allocated by the body decoder (readPutBody), the
	// read loop never reuses it, and the local.Put above consumed it through
	// a fresh bytes.Reader — nothing mutates or aliases it after this point.
	// The old defensive copy doubled transient memory during PUT bursts.
	if s.remote != nil {
		data := req.Body
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			semStart := time.Now()
			s.putSem <- struct{}{} // acquire
			s.Latency.SemWait.Record(time.Since(semStart))
			defer func() { <-s.putSem }() // release
			remotePutStart := time.Now()
			err := s.remote.Put(actionID, outputID, bytes.NewReader(data), int64(len(data)))
			s.Latency.RemotePut.Record(time.Since(remotePutStart))
			if err != nil {
				if !errors.Is(err, errLogged) {
					logger.WithSubsystem("cache").Debug("remote put: %v", err)
				}
			} else {
				s.sendStat(StatEvent{RemotePut: 1})
			}
		}()
	}

	return Response{
		ID:       req.ID,
		DiskPath: diskPath,
	}
}

func hexToBytes(h string) []byte {
	// hex.DecodeString, not 32 reflective fmt.Sscanf calls per GET-hit
	// response. On malformed input it returns the bytes decoded so far,
	// matching the old best-effort behavior.
	b, _ := hex.DecodeString(h)
	return b
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// describeFile reads the first 1024 bytes of a cached object on disk and
// returns a human-readable label via describeData. Used in debug logs to
// decode what a given actionID actually represents.
func describeFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return "unknown"
	}
	defer f.Close()
	header := make([]byte, 1024)
	n, _ := f.Read(header)
	return describeData(header[:n])
}
