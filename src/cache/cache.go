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

// staleKeyForgetter drops a stale "already present" claim after a PUT
// replace, so the next remote Put uploads instead of skipping it.
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
	BatchUse  uint32 `json:"bu,omitempty"` // prefetched entries this local hit read back

	Latency *LatencyStatsSnapshot `json:"lat,omitempty"` // flush latency on close

	// Web is a standalone cacheprog's web tier, sent as it closes; nothing else tells the parent what the remote did for it.
	Web *WebSummary `json:"web,omitempty"`

	// Per-action outcome, piggybacked on the get/put counter events. All fields optional, so old senders and listeners stay wire-compatible.
	Action  string `json:"a,omitempty"`  // truncated actionID (see truncateActionID)
	Op      string `json:"op,omitempty"` // "get" | "put"
	Outcome string `json:"o,omitempty"`  // "hit-local" | "hit-remote" | "miss" | "put"
	Bytes   int64  `json:"b,omitempty"`  // object size in bytes
	DurUS   int64  `json:"d,omitempty"`  // operation duration, microseconds
}

// maxConcurrentPuts matches the HTTP transport's MaxConnsPerHost to avoid connection churn.
const maxConcurrentPuts = 64

// lockShards caps the per-action mutex table; a shard collision only coarsens locking, which is safe.
const lockShards = 256

// Server implements the GOCACHEPROG JSON-over-stdio protocol.
type Server struct {
	local  LocalStore
	remote IBackend // nil if no remote backend configured
	// keyNamespace suffixes every derived action key (see actionKey); only the standalone path sets it.
	keyNamespace string
	locks        [lockShards]sync.Mutex
	wg           sync.WaitGroup // tracks in-flight async remote puts
	putSem       chan struct{}  // semaphore bounding concurrent remote puts
	Misses       AtomicCounter
	batch        BatchStats
	// Shared across sibling Servers: callback and GET land on different conns.
	prefetchedKeys *prefetchSet

	Latency   LatencyStats
	statsConn net.Conn // persistent connection to parent's stats socket
	statsMu   sync.Mutex

	// IndexPutsRefused counts module-index PUTs the sink answered (see isGoModuleIndex). Large on any run; not a poison signal.
	IndexPutsRefused AtomicCounter
	// sinkDir holds refused index bodies; created on the earliest refusal, removed on Run's return.
	sinkMu  sync.Mutex
	sinkDir string
}

// NewServer creates a cache server. remote may be nil for local-only mode.
// Connects to the stats socket if GOCACHE_STATS_SOCK is set.
//
// For standalone mode (direct WebBackend), this also wires up batch
// callbacks. In daemon mode, use Daemon.wireBatchCallbacks instead —
// callbacks must be set a single time on the shared WebBackend, not per-connection.
func NewServer(local LocalStore, remote IBackend) *Server {
	s := &Server{
		local:          local,
		remote:         remote,
		putSem:         make(chan struct{}, maxConcurrentPuts),
		prefetchedKeys: newPrefetchSet(),
	}
	// Standalone only: daemon mode wires this on the shared WebBackend instead, to avoid a per-connection race.
	if wb, ok := remote.(*WebBackend); ok {
		wb.Latency = &s.Latency
		wireBatchCallbacks(wb, local, s)
	}
	if sock := os.Getenv("GOCACHE_STATS_SOCK"); sock != "" {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			// Wait for the accept-ack: a unix dial succeeds before accept runs, so stat events could drop into an unread queue.
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
		// e.Key carries the full cache key; LocalCache keys on the bare action ID, so strip the prefix.
		keyPrefix := wb.KeyPrefix()
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
			// A hash mismatch means a corrupt entry; skip it and let the real GET self-heal.
			if _, ok := outputIDMatches(e.OutputID, decompressed); !ok {
				continue
			}
			// A build-id mismatch is cross-contamination outputIDMatches cannot catch.
			if _, ok := buildIDMatchesAction(actionID, decompressed); !ok {
				continue
			}
			// A module index cannot be verified against this key; cmd/go recomputes it locally anyway.
			if isGoModuleIndex(decompressed) {
				continue
			}
			// PutIfAbsent, never Put: this runs unserialized against GET/PUT RPC handlers, so a plain Put could race and lose.
			stored, err := local.PutIfAbsent(actionID, e.OutputID, bytes.NewReader(decompressed))
			if err != nil || !stored {
				continue
			}
			sink.prefetched().add(actionID)
			populated++
		}
		if populated > 0 {
			sink.recordBatchPop(populated)
		}
	}
}

// statsSink lets batch callbacks wire to a Server or a Daemon stats connection.
type statsSink interface {
	recordBatchPop(n uint32)
	// prefetched is the set the GET path takes entries back out of.
	prefetched() *prefetchSet
}

func (s *Server) recordBatchPop(n uint32) {
	s.batch.Populated.Add(n)
	s.sendStat(StatEvent{BatchPop: n})
}

func (s *Server) prefetched() *prefetchSet { return s.prefetchedKeys }

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
	// CloseWrite drains buffered data; a full Close here could race handleConn
	// and close the fd early.
	if uc, ok := s.statsConn.(*net.UnixConn); ok {
		uc.CloseWrite()
	} else {
		s.statsConn.Close()
	}
}

// BatchStats tracks batch GET prefetch metrics.
type BatchStats struct {
	Populated AtomicCounter `json:"populated"` // entries prefetched into local cache
	Used      AtomicCounter `json:"used"`      // prefetched entries a later local hit read
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

	// Thread-safe: multiple goroutines respond concurrently for parallel GETs.
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
			s.removeIndexSink()
			return nil

		case CmdPut:
			// PUT body follows on the next non-empty line as base64 (cmd/go
			// writes the JSON line, a blank line, then '"'+base64+'"'; raw
			// unquoted base64 is also accepted — see readloop.go). The body
			// must be read synchronously before the next request. The line
			// is read in full whatever its length, so bodies past the old
			// scanner cap no longer kill the protocol loop.
			if req.BodySize > 0 {
				body, err := readPutBody(br, req.BodySize)
				if err != nil {
					if bad := (*badPutBodyError)(nil); errors.As(err, &bad) {
						// Stream stays line-aligned: fail only this PUT, store nothing, keep serving.
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
	s.removeIndexSink()
	return readErr
}

// SetKeyNamespace scopes every derived action key to namespace. Call before Run.
func (s *Server) SetKeyNamespace(namespace string) {
	s.keyNamespace = CanonicalKeyNamespace(namespace)
}

// actionKey is the single choke point turning a raw ActionID into a store
// key (hex plus namespace); stat events keep the raw ID for profile joins.
func (s *Server) actionKey(rawActionID []byte) string {
	key := fmt.Sprintf("%x", rawActionID)
	if s.keyNamespace != "" {
		key += s.keyNamespace
	}
	return key
}

// lock returns key's shard mutex. A collision on the fixed shard table only
// coarsens serialization; it is always safe. Allocation-free inline FNV-1a.
func (s *Server) lock(key string) *sync.Mutex {
	h := uint32(2166136261)
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return &s.locks[h%lockShards]
}

// flushLatency reports this Server's trackers plus, in standalone mode, the
// HTTP pool and the web tier. A daemon connection's remote is the no-close
// wrapper, so the assertion fails and the Daemon reports those itself.
func (s *Server) flushLatency() {
	snap := s.Latency.Snapshot()
	ev := StatEvent{Latency: &snap}
	if wb, ok := s.remote.(*WebBackend); ok {
		snap.Pool = wb.Pool.Snapshot()
		ws := wb.SummarySnapshot()
		ev.Web = &ws
	}
	s.sendStat(ev)
}
