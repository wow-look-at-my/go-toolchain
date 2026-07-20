package cache

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// Cmd is a GOCACHEPROG command verb.
type Cmd string

const (
	CmdGet   Cmd = "get"
	CmdPut   Cmd = "put"
	CmdClose Cmd = "close"
)

// Request is a GOCACHEPROG protocol request from the Go toolchain.
type Request struct {
	ID       int64  `json:"ID"`
	Command  Cmd    `json:"Command"`
	ActionID []byte `json:"ActionID"`
	OutputID []byte `json:"OutputID,omitempty"`
	Body     []byte `json:"-"` // populated from next line for PUTs
	BodySize int64  `json:"BodySize,omitempty"`
}

// Response is a GOCACHEPROG protocol response.
type Response struct {
	ID            int64      `json:"ID"`
	Err           string     `json:"Err,omitempty"`
	Miss          bool       `json:"Miss,omitempty"`
	OutputID      []byte     `json:"OutputID,omitempty"`
	DiskPath      string     `json:"DiskPath,omitempty"`
	Size          int64      `json:"Size,omitempty"`
	Time          *time.Time `json:"Time,omitempty"`
	KnownCommands []Cmd      `json:"KnownCommands,omitempty"`
}

// IBackend is the interface for a remote cache store.
type IBackend interface {
	Get(actionID string) (outputID string, body io.ReadCloser, size int64, t time.Time, miss bool, err error)
	Put(actionID, outputID string, body io.Reader, bodySize int64) error
	Close() error
	GetStats() *CacheStats
}

// staleKeyForgetter is the optional backend capability the PUT replace path
// uses: when a fresh cmd/go PUT overwrites a local entry whose outputID
// disagreed (a mis-keyed or stale body), the remote's optimistic "already
// present" claim for that key is stale too — the object the server holds (or
// that this client believes it holds) is NOT the content cmd/go just
// computed. Forgetting the claim lets the immediately following remote Put
// actually upload the fresh body instead of skipping it as known
// (put-skipped: known), so the shared tier self-heals alongside the local
// one. Module indexes still never upload (webput's content guard), which is
// correct — they are refused on every read path too.
type staleKeyForgetter interface {
	ForgetStale(actionID string)
}

// StatEvent is a single counter increment sent over the stats socket.
type StatEvent struct {
	LocalHit  uint32 `json:"lh,omitempty"`
	LocalPut  uint32 `json:"lp,omitempty"`
	RemoteHit uint32 `json:"rh,omitempty"`
	RemotePut uint32 `json:"rp,omitempty"`
	Miss      uint32 `json:"m,omitempty"`
	BatchPop  uint32 `json:"bp,omitempty"` // entries prefetched into local cache from batch GET

	Latency *LatencyStatsSnapshot `json:"lat,omitempty"` // flush latency on close

	// Per-action outcome, piggybacked on the counter events handleGet and
	// handlePut already emit (no extra socket writes). All optional: an old
	// listener ignores them and an old sender simply never sets them, so the
	// wire format stays compatible in both directions.
	Action  string `json:"a,omitempty"`  // 20-char truncated actionID (base64.RawURLEncoding(id[:15]))
	Op      string `json:"op,omitempty"` // "get" | "put"
	Outcome string `json:"o,omitempty"`  // "hit-local" | "hit-remote" | "miss" | "put"
	Bytes   int64  `json:"b,omitempty"`  // object size in bytes
	DurUS   int64  `json:"d,omitempty"`  // operation duration, microseconds
}

// maxConcurrentPuts is the maximum number of concurrent remote put operations.
// Matches the HTTP transport's MaxConnsPerHost to avoid connection churn.
const maxConcurrentPuts = 64

// lockShards is the size of the fixed per-action mutex table. The old
// map[string]*sync.Mutex grew one entry per unique actionID for the
// connection's lifetime (tens of MB across daemon connections on 100k-action
// builds) and was never pruned. A fixed sharded table caps that at a constant:
// hash collisions merely serialize two unrelated actions occasionally, which
// is always safe.
const lockShards = 256

// Server implements the GOCACHEPROG JSON-over-stdio protocol.
type Server struct {
	local     LocalStore
	remote    IBackend // nil if no remote backend configured
	locks     [lockShards]sync.Mutex
	wg        sync.WaitGroup // tracks in-flight async remote puts
	putSem    chan struct{}  // semaphore bounding concurrent remote puts
	Misses    AtomicCounter
	batch     BatchStats
	Latency   LatencyStats
	statsConn net.Conn // persistent connection to parent's stats socket
	statsMu   sync.Mutex
}

// NewServer creates a cache server. remote may be nil for local-only mode.
// Connects to the stats socket if GOCACHE_STATS_SOCK is set.
//
// For standalone mode (direct WebBackend), this also wires up batch
// callbacks. In daemon mode, use Daemon.wireBatchCallbacks instead —
// callbacks must be set once on the shared WebBackend, not per-connection.
func NewServer(local LocalStore, remote IBackend) *Server {
	s := &Server{
		local:  local,
		remote: remote,
		putSem: make(chan struct{}, maxConcurrentPuts),
	}
	// Wire sub-operation latency tracking and batch callbacks for standalone
	// mode (direct WebBackend) only. In daemon mode the remote is wrapped in
	// noCloseBackend and the Daemon wires BOTH once on the shared WebBackend:
	// re-pointing wb.Latency here per connection was an unsynchronized write
	// to shared state that raced every other connection's in-flight web ops.
	if wb, ok := remote.(*WebBackend); ok {
		wb.Latency = &s.Latency
		wireBatchCallbacks(wb, local, s)
	}
	if sock := os.Getenv("GOCACHE_STATS_SOCK"); sock != "" {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			// Wait for the listener's accept-ack. A unix dial succeeds as
			// soon as the kernel queues the connection — reading the ack
			// guarantees the listener has accepted it and registered its
			// reader, so stat events cannot be dropped in the accept queue.
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			var ack [1]byte
			if _, err := conn.Read(ack[:]); err == nil {
				conn.SetReadDeadline(time.Time{})
				s.statsConn = conn
			} else {
				conn.Close()
			}
		}
	}
	return s
}

// wireBatchCallbacks sets up the OnBatchEntries callback on a WebBackend.
// When a batch GET returns prefetch entries, this callback writes them to
// the local cache so future GETs hit locally.
func wireBatchCallbacks(wb *WebBackend, local LocalStore, sink statsSink) {
	wb.OnBatchEntries = func(entries []BatchEntry) {
		var populated uint32
		// e.Key is the full cache key (e.g. "go-buildcache/v1abcdef...").
		// LocalCache is keyed by the bare action ID ("abcdef..."), which is
		// what Server.handleGet uses. Strip the prefix so the paths match.
		keyPrefix := wb.prefix + "v1"
		for _, e := range entries {
			if e.OutputID == "" {
				continue
			}
			actionID := strings.TrimPrefix(e.Key, keyPrefix)
			if actionID == e.Key {
				continue // unexpected key format; skip
			}
			if _, miss := local.Peek(actionID); !miss {
				continue // already cached (Peek: prefetch is not a cache hit)
			}
			// The data from the server is LZ4-compressed (same as individual GETs).
			decompressed, err := decompressData(e.Data)
			if err != nil {
				continue
			}
			// Never prefetch a body that does not hash to its outputID into the
			// local pack: a corrupt entry would then be served as a "valid"
			// local hit and fail the build ("corrupt index"). Skip it — the
			// real GET for this key re-fetches and self-heals if needed.
			if _, ok := outputIDMatches(e.OutputID, decompressed); !ok {
				continue
			}
			// Never prefetch a compiled object whose build id belongs to a
			// different action than its key (cross-contamination the outputID
			// hash cannot catch -- see buildIDMatchesAction). Populating it would
			// seed a local hit that serves the wrong package's export data.
			if _, ok := buildIDMatchesAction(actionID, decompressed); !ok {
				continue
			}
			// Never prefetch a Go module index into the local pack: it cannot be
			// verified to belong under this key (see isGoModuleIndex), and a
			// mis-keyed one seeded as a local hit breaks package loading
			// ("package runtime is not in std" / "corrupt index"). cmd/go
			// recomputes the index locally, so skipping the prefetch is free.
			// This filter is LOAD-BEARING: the local tier serves module indexes
			// on the trust that they are locally-originated (see verify.go), so
			// no web-originated body may carry one into the local store — here,
			// or on the individual/batch GET paths (web.go / batch.go).
			if isGoModuleIndex(decompressed) {
				continue
			}
			// PutIfAbsent, never Put: the Peek above is only an optimization,
			// and this callback runs on the batch coalescer's goroutine with no
			// per-action serialization against the GET/PUT RPC handlers. A
			// plain Put here could race a concurrent cmd/go PUT for the same
			// action and — depending on which side committed last — replace a
			// locally-computed body with this web-originated one, either in the
			// live index or (worse) only in the pack file's replay order, where
			// the poison surfaces on the NEXT build as "corrupt index". The
			// atomic if-absent store makes the local cmd/go's data always win.
			stored, err := local.PutIfAbsent(actionID, e.OutputID, bytes.NewReader(decompressed))
			if err != nil || !stored {
				continue
			}
			populated++
		}
		if populated > 0 {
			sink.recordBatchPop(populated)
		}
	}
}

// statsSink abstracts stat recording so batch callbacks can be wired to
// either a per-connection Server or a long-lived Daemon stats connection.
type statsSink interface {
	recordBatchPop(n uint32)
}

func (s *Server) recordBatchPop(n uint32) {
	s.batch.Populated.Add(n)
	s.sendStat(StatEvent{BatchPop: n})
}

// sendStat sends a single stat event to the parent over the persistent connection.
func (s *Server) sendStat(ev StatEvent) {
	if s.statsConn == nil {
		return
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	s.statsMu.Lock()
	s.statsConn.Write(append(data, '\n'))
	s.statsMu.Unlock()
}

func (s *Server) closeStats() {
	if s.statsConn == nil {
		return
	}
	// Use CloseWrite to signal EOF to the reader while allowing buffered
	// data to drain. A full Close() here would race with the listener's
	// handleConn goroutine, potentially closing the fd before the reader
	// finishes consuming the buffered stat events.
	if uc, ok := s.statsConn.(*net.UnixConn); ok {
		uc.CloseWrite()
	} else {
		s.statsConn.Close()
	}
}

// BatchStats tracks batch GET prefetch metrics.
type BatchStats struct {
	Populated AtomicCounter `json:"populated"` // entries prefetched into local cache
}

// ServerStats is the serialized aggregate of all cache layer stats.
// Fields are pointers to the live atomic counters — no copying.
type ServerStats struct {
	Local   *CacheStats         `json:"local"`
	Remote  *CacheStats         `json:"remote,omitempty"`
	Misses  *AtomicCounter      `json:"misses"`
	Batch   *BatchStats         `json:"batch,omitempty"`
	Latency *LatencyStats       `json:"latency,omitempty"`
	Pool    *ConcurrencyTracker `json:"pool,omitempty"`
}

// GetStats returns pointers to the live cache layer stats.
func (s *Server) GetStats() *ServerStats {
	ss := &ServerStats{
		Local:   s.local.StatsPtr(),
		Misses:  &s.Misses,
		Batch:   &s.batch,
		Latency: &s.Latency,
	}
	if s.remote != nil {
		ss.Remote = s.remote.GetStats()
	}
	return ss
}

// Run starts the protocol loop, reading requests from r and writing
// responses to w. It blocks until the input stream closes or a close
// command is received.
//
// GET requests are dispatched to goroutines and processed concurrently.
// The GOCACHEPROG protocol allows out-of-order responses — each response
// carries the request ID and the Go toolchain matches them via a map of
// per-request channels (see cmd/go/internal/cache/prog.go).
func (s *Server) Run(r io.Reader, w io.Writer) error {
	// Handshake: announce supported commands.
	enc := json.NewEncoder(w)
	if err := enc.Encode(Response{
		KnownCommands: []Cmd{CmdGet, CmdPut, CmdClose},
	}); err != nil {
		return fmt.Errorf("handshake: %w", err)
	}

	br := bufio.NewReaderSize(r, 64*1024)

	// Thread-safe response writer — multiple goroutines may respond
	// concurrently for parallel GETs.
	var writeMu sync.Mutex
	writeResp := func(resp Response) {
		writeMu.Lock()
		enc.Encode(resp)
		writeMu.Unlock()
	}

	// Track in-flight GET goroutines so we can drain them on close.
	var getWg sync.WaitGroup

	var readErr error
loop:
	for {
		line, err := readProtoLine(br, 0)
		if err != nil {
			if err != io.EOF {
				readErr = err
			}
			break
		}
		if len(line) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}

		switch req.Command {
		case CmdClose:
			getWg.Wait() // drain in-flight GETs
			writeResp(Response{ID: req.ID})
			s.wg.Wait() // drain in-flight remote puts
			if s.remote != nil {
				s.remote.Close()
			}
			s.flushLatency()
			s.closeStats()
			return nil

		case CmdPut:
			// PUT body follows on the next non-empty line as base64 (cmd/go
			// writes the JSON line, a blank line, then '"'+base64+'"'; raw
			// unquoted base64 is also accepted — see readloop.go). The body
			// must be read synchronously before the next request. The line
			// is read in full whatever its length, so bodies past the old
			// 64 MiB scanner cap no longer kill the protocol loop.
			if req.BodySize > 0 {
				body, err := readPutBody(br, req.BodySize)
				if err != nil {
					if bad := (*badPutBodyError)(nil); errors.As(err, &bad) {
						// The stream is still line-aligned: fail only this
						// PUT and store NOTHING — an empty or truncated body
						// committed under the real actionID/outputID would be
						// served as a "valid" hit forever — then keep serving.
						writeResp(Response{ID: req.ID, Err: "cacheprog: put body: " + bad.Error()})
						continue
					}
					// Stream-level failure (EOF mid-request or a read error):
					// stop serving. Nothing was stored for this PUT.
					if err != io.EOF {
						readErr = err
					}
					break loop
				}
				req.Body = body
			}
			writeResp(s.handlePut(req))

		case CmdGet:
			getWg.Add(1)
			go func() {
				defer getWg.Done()
				writeResp(s.handleGet(req))
			}()
		}
	}

	// Input closed without explicit close command — still drain.
	getWg.Wait()
	s.wg.Wait()
	if s.remote != nil {
		s.remote.Close()
	}
	s.flushLatency()
	s.closeStats()
	return readErr
}

// lock returns the shard mutex for key. Distinct keys may share a shard
// (coarser serialization on a collision — always safe); the same key always
// maps to the same mutex. Allocation-free inline FNV-1a.
func (s *Server) lock(key string) *sync.Mutex {
	h := uint32(2166136261)
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return &s.locks[h%lockShards]
}

// flushLatency sends a final latency snapshot over the stats socket. It
// covers only this Server's own trackers; in standalone mode (direct
// WebBackend) the shared HTTP-pool usage is attached too. In daemon mode the
// Daemon reports the shared pool and web-op latencies exactly once at Close —
// each connection flushing the shared CUMULATIVE pool snapshot made the
// listener, which merges snapshots additively, overcount it N-fold.
func (s *Server) flushLatency() {
	snap := s.Latency.Snapshot()
	if wb, ok := s.remote.(*WebBackend); ok {
		snap.Pool = wb.Pool.Snapshot()
	}
	s.sendStat(StatEvent{Latency: &snap})
}
