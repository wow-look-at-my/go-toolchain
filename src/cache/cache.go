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
	"sync/atomic"
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

// StatEvent is a single counter increment sent over the stats socket.
type StatEvent struct {
	LocalHit  uint32 `json:"lh,omitempty"`
	LocalPut  uint32 `json:"lp,omitempty"`
	RemoteHit uint32 `json:"rh,omitempty"`
	RemotePut uint32 `json:"rp,omitempty"`
	Miss      uint32 `json:"m,omitempty"`
	BatchPop  uint32 `json:"bp,omitempty"` // entries prefetched into local cache from batch GET

	Latency *LatencyStatsSnapshot `json:"lat,omitempty"` // flush latency on close
}

// maxConcurrentPuts is the maximum number of concurrent remote put operations.
// Matches the HTTP transport's MaxConnsPerHost to avoid connection churn.
const maxConcurrentPuts = 64

// Server implements the GOCACHEPROG JSON-over-stdio protocol.
type Server struct {
	local     LocalStore
	remote    IBackend // nil if no remote backend configured
	mu        sync.Mutex
	locks     map[string]*sync.Mutex
	wg        sync.WaitGroup // tracks in-flight async remote puts
	putSem    chan struct{}  // semaphore bounding concurrent remote puts
	Misses    AtomicCounter
	batch     BatchStats
	Latency   LatencyStats
	statsConn net.Conn // persistent connection to parent's stats socket
	statsMu   sync.Mutex
	debug     bool // log hits/misses to stderr
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
		locks:  make(map[string]*sync.Mutex),
		putSem: make(chan struct{}, maxConcurrentPuts),
		debug:  os.Getenv("GOCACHE_DEBUG") == "1",
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
			if _, miss := local.Get(actionID); !miss {
				continue // already cached
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
			if isGoModuleIndex(decompressed) {
				continue
			}
			local.Put(actionID, e.OutputID, bytes.NewReader(decompressed))
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

// StatsListener listens on a unix domain socket and aggregates streaming
// stat events from all cacheprog subprocesses in real-time.
type StatsListener struct {
	listener  net.Listener
	path      string
	local     CacheStats
	remote    CacheStats
	batch     BatchStats
	misses    AtomicCounter
	latency   LatencyStats
	pool      ConcurrencyTracker
	hasRemote atomic.Bool // true if a remote backend was configured (set by caller)
	hasBatch  atomic.Bool
	wg        sync.WaitGroup // tracks active handleConn goroutines
	acceptWg  sync.WaitGroup // tracks the accept loop goroutine
}

// SetHasRemote marks the listener as having a remote backend configured.
// This controls whether Stats() includes the Remote field, regardless of
// whether any remote events have actually been received yet.
func (sl *StatsListener) SetHasRemote() {
	sl.hasRemote.Store(true)
}

// NewStatsListener creates a unix socket and starts accepting connections.
func NewStatsListener(path string) (*StatsListener, error) {
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	sl := &StatsListener{listener: ln, path: path}
	sl.acceptWg.Add(1)
	go func() {
		defer sl.acceptWg.Done()
		sl.accept()
	}()
	return sl, nil
}

func (sl *StatsListener) accept() {
	for {
		conn, err := sl.listener.Accept()
		if err != nil {
			return
		}
		sl.wg.Add(1)
		// Ack the connection so the dialer knows it has been picked up.
		// Once the dialer has read this byte, this connection is registered
		// in sl.wg, so Close()'s wg.Wait deterministically covers it.
		conn.Write([]byte{0})
		go sl.handleConn(conn)
	}
}

func (sl *StatsListener) handleConn(conn net.Conn) {
	defer sl.wg.Done()
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var ev StatEvent
		if json.Unmarshal(scanner.Bytes(), &ev) != nil {
			continue
		}
		if ev.LocalHit > 0 {
			sl.local.Hits.Add(ev.LocalHit)
		}
		if ev.LocalPut > 0 {
			sl.local.Puts.Add(ev.LocalPut)
		}
		if ev.RemoteHit > 0 {
			sl.hasRemote.Store(true)
			sl.remote.Hits.Add(ev.RemoteHit)
		}
		if ev.RemotePut > 0 {
			sl.hasRemote.Store(true)
			sl.remote.Puts.Add(ev.RemotePut)
		}
		if ev.Miss > 0 {
			sl.misses.Add(ev.Miss)
		}
		if ev.BatchPop > 0 {
			sl.hasBatch.Store(true)
			sl.batch.Populated.Add(ev.BatchPop)
		}
		if ev.Latency != nil {
			sl.latency.Merge(*ev.Latency)
			sl.pool.Merge(ev.Latency.Pool)
		}
	}
}

// Close stops the listener, waits for all connections to drain, and cleans up.
//
// Delivery is guaranteed by the accept-ack handshake: a dialer only keeps its
// stats connection after reading the listener's 1-byte ack, which the accept
// loop writes AFTER registering the connection in sl.wg — so wg.Wait() below
// deterministically covers every connection whose dialer ever sent an event.
// The 10ms accept-deadline drain is belt-and-suspenders for a dialer racing
// Close itself: such a dialer degrades to stats-off via its ack timeout
// instead of silently losing data.
func (sl *StatsListener) Close() {
	// Give the accept loop a short window to drain any connections that are
	// already in the kernel accept queue. listener.Close() would drop them.
	if ul, ok := sl.listener.(*net.UnixListener); ok {
		ul.SetDeadline(time.Now().Add(10 * time.Millisecond))
	} else {
		sl.listener.Close()
	}
	sl.acceptWg.Wait() // wait for accept loop to exit after draining queue
	sl.wg.Wait()       // wait for all handleConn goroutines to finish
	sl.listener.Close()
	os.Remove(sl.path)
}

// Stats returns the aggregated stats.
func (sl *StatsListener) Stats() *ServerStats {
	ss := &ServerStats{
		Local:   &sl.local,
		Misses:  &sl.misses,
		Latency: &sl.latency,
		Pool:    &sl.pool,
	}
	if sl.hasRemote.Load() {
		ss.Remote = &sl.remote
	}
	if sl.hasBatch.Load() {
		ss.Batch = &sl.batch
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

func (s *Server) lock(key string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.locks[key]
	if !ok {
		m = &sync.Mutex{}
		s.locks[key] = m
	}
	return m
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

func (s *Server) handleGet(req Request) Response {
	actionID := fmt.Sprintf("%x", req.ActionID)
	mu := s.lock(actionID)

	lockStart := time.Now()
	mu.Lock()
	s.Latency.LockWait.Record(time.Since(lockStart))
	defer mu.Unlock()

	// Check local cache first.
	localStart := time.Now()
	meta, miss := s.local.Get(actionID)
	s.Latency.LocalGet.Record(time.Since(localStart))

	if !miss {
		s.sendStat(StatEvent{LocalHit: 1})
		if s.debug {
			fmt.Fprintf(os.Stderr, "cache: HIT local  %s output=%s size=%d\n", actionID, shortID(meta.OutputID), meta.Size)
		}
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
		s.sendStat(StatEvent{Miss: 1})
		if s.debug {
			fmt.Fprintf(os.Stderr, "cache: MISS       %s\n", actionID)
		}
		return Response{ID: req.ID, Miss: true}
	}

	remoteStart := time.Now()
	outputID, body, _, t, remoteMiss, err := s.remote.Get(actionID)

	if err != nil || remoteMiss {
		s.Misses.Increment()
		s.sendStat(StatEvent{Miss: 1})
		if s.debug {
			if err != nil {
				fmt.Fprintf(os.Stderr, "cache: MISS       %s (remote error: %v)\n", actionID, err)
			} else {
				fmt.Fprintf(os.Stderr, "cache: MISS       %s\n", actionID)
			}
		}
		return Response{ID: req.ID, Miss: true}
	}
	s.Latency.RemoteGet.Record(time.Since(remoteStart))
	s.sendStat(StatEvent{RemoteHit: 1})
	defer body.Close()

	// Write to local cache for future hits.
	localPutStart := time.Now()
	diskPath, err := s.local.Put(actionID, outputID, body)
	s.Latency.LocalPut.Record(time.Since(localPutStart))

	if err != nil {
		return Response{ID: req.ID, Miss: true}
	}

	if s.debug {
		fmt.Fprintf(os.Stderr, "cache: HIT remote %s [%s] output=%s\n", actionID, describeFile(diskPath), shortID(outputID))
	}

	return Response{
		ID:       req.ID,
		OutputID: hexToBytes(outputID),
		DiskPath: diskPath,
		Size:     fileSize(diskPath),
		Time:     &t,
	}
}

func (s *Server) handlePut(req Request) Response {
	actionID := fmt.Sprintf("%x", req.ActionID)
	outputID := fmt.Sprintf("%x", req.OutputID)
	mu := s.lock(actionID)

	lockStart := time.Now()
	mu.Lock()
	s.Latency.LockWait.Record(time.Since(lockStart))
	defer mu.Unlock()

	// Check if already cached locally.
	localStart := time.Now()
	meta, miss := s.local.Get(actionID)
	s.Latency.LocalGet.Record(time.Since(localStart))

	if !miss {
		if s.debug {
			fmt.Fprintf(os.Stderr, "cache: PUT  dedup  %s output=%s\n", actionID, shortID(meta.OutputID))
		}
		return Response{ID: req.ID, DiskPath: meta.DiskPath, Size: meta.Size}
	}

	body := bytes.NewReader(req.Body)

	localPutStart := time.Now()
	diskPath, err := s.local.Put(actionID, outputID, body)
	s.Latency.LocalPut.Record(time.Since(localPutStart))

	if err != nil {
		return Response{ID: req.ID, Err: err.Error()}
	}
	s.sendStat(StatEvent{LocalPut: 1})
	if s.debug {
		fmt.Fprintf(os.Stderr, "cache: PUT  new    %s [%s] size=%d\n", actionID, describeData(req.Body), len(req.Body))
	}
	// Async write to remote. The semaphore bounds concurrency to avoid
	// connection churn — each goroutine reuses a pooled HTTP connection
	// instead of creating (and discarding) a new TCP+TLS connection.
	if s.remote != nil {
		data := make([]byte, len(req.Body))
		copy(data, req.Body)
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
					fmt.Fprintf(os.Stderr, "cacheprog: remote put: %v\n", err)
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

func hexToBytes(hex string) []byte {
	b := make([]byte, len(hex)/2)
	for i := 0; i < len(b); i++ {
		fmt.Sscanf(hex[i*2:i*2+2], "%02x", &b[i])
	}
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
