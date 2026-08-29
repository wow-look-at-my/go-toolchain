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

	// Check the local cache before the remote.
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

	// Write to local cache; the remote body already passed web ingestion guards (sha256, build-id, module-index).
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

	// A module index never enters the store; its content-free key means a served body cannot be verified (see isGoModuleIndex).
	if isGoModuleIndex(req.Body) {
		return s.refuseIndexPut(req, actionID, outputID)
	}

	mu := s.lock(actionID)

	lockStart := time.Now()
	mu.Lock()
	s.Latency.LockWait.Record(time.Since(lockStart))
	defer mu.Unlock()

	// Dedup check via Peek, not Get: serving what the caller just recomputed is not a real cache hit.
	localStart := time.Now()
	meta, miss := s.local.Peek(actionID)
	s.Latency.LocalGet.Record(time.Since(localStart))

	cacheLog := logger.WithSubsystem("cache")
	// Dedup only when the stored entry matches what cmd/go just computed; a differing outputID
	// must be overwritten, since cmd/go owns its own action keys.
	if !miss && meta.OutputID == outputID {
		cacheLog.Debug("PUT  dedup  %s output=%s", actionID, shortID(meta.OutputID))
		return Response{ID: req.ID, DiskPath: meta.DiskPath, Size: meta.Size}
	}
	if !miss {
		cacheLog.Debug("PUT  replace %s stored-output=%s incoming-output=%s (stored entry does not match; overwriting)",
			actionID, shortID(meta.OutputID), shortID(outputID))
		// The remote claim is as stale as local; the Put below must re-upload, not skip an
		// already-present body (see staleKeyForgetter).
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
	// hex.DecodeString, not a reflective fmt.Sscanf per byte; on malformed input it returns bytes decoded so far.
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

// describeFile reads the leading bytes of a cached object on disk and
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
