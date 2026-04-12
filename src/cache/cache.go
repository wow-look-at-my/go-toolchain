package cache

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
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

	Latency *LatencyStatsSnapshot `json:"lat,omitempty"` // flush latency on close
}

// maxConcurrentPuts is the maximum number of concurrent remote put operations.
// Matches the HTTP transport's MaxConnsPerHost to avoid connection churn.
const maxConcurrentPuts = 64

// Server implements the GOCACHEPROG JSON-over-stdio protocol.
type Server struct {
	local    *LocalCache
	remote   IBackend // nil if no remote backend configured
	mu       sync.Mutex
	locks    map[string]*sync.Mutex
	wg       sync.WaitGroup    // tracks in-flight async remote puts
	putSem   chan struct{}      // semaphore bounding concurrent remote puts
	Misses   AtomicCounter
	Latency  LatencyStats
	statsConn net.Conn // persistent connection to parent's stats socket
	statsMu   sync.Mutex
	debug    bool // log hits/misses to stderr
}

// NewServer creates a cache server. remote may be nil for local-only mode.
// Connects to the stats socket if GOCACHE_STATS_SOCK is set.
func NewServer(local *LocalCache, remote IBackend) *Server {
	s := &Server{
		local:  local,
		remote: remote,
		locks:  make(map[string]*sync.Mutex),
		putSem: make(chan struct{}, maxConcurrentPuts),
		debug:  os.Getenv("GOCACHE_DEBUG") == "1",
	}
	// Wire sub-operation latency tracking into the web backend.
	if wb, ok := remote.(*WebBackend); ok {
		wb.Latency = &s.Latency
	} else if nc, ok := remote.(*noCloseBackend); ok {
		if wb, ok := nc.IBackend.(*WebBackend); ok {
			wb.Latency = &s.Latency
		}
	}
	if sock := os.Getenv("GOCACHE_STATS_SOCK"); sock != "" {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			s.statsConn = conn
		}
	}
	return s
}

// logMiss records a missed action ID to stderr for diagnostics.
func (s *Server) logMiss(actionID string) {
	fmt.Fprintf(os.Stderr, "cache: MISS %s\n", actionID)
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

// ServerStats is the serialized aggregate of all cache layer stats.
// Fields are pointers to the live atomic counters — no copying.
type ServerStats struct {
	Local   *CacheStats         `json:"local"`
	Remote  *CacheStats         `json:"remote,omitempty"`
	Misses  *AtomicCounter      `json:"misses"`
	Latency *LatencyStats       `json:"latency,omitempty"`
	Pool    *ConcurrencyTracker `json:"pool,omitempty"`
}

// GetStats returns pointers to the live cache layer stats.
func (s *Server) GetStats() *ServerStats {
	ss := &ServerStats{
		Local:   &s.local.Stats,
		Misses:  &s.Misses,
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
	listener net.Listener
	path     string
	local    CacheStats
	remote   CacheStats
	misses   AtomicCounter
	latency  LatencyStats
	pool     ConcurrencyTracker
	hasRemote atomic.Bool
	wg       sync.WaitGroup
}

// NewStatsListener creates a unix socket and starts accepting connections.
func NewStatsListener(path string) (*StatsListener, error) {
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	sl := &StatsListener{listener: ln, path: path}
	go sl.accept()
	return sl, nil
}

func (sl *StatsListener) accept() {
	for {
		conn, err := sl.listener.Accept()
		if err != nil {
			return
		}
		sl.wg.Add(1)
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
		if ev.Latency != nil {
			sl.latency.Merge(*ev.Latency)
			sl.pool.Merge(ev.Latency.Pool)
		}
	}
}

// Close stops the listener, waits for all connections to drain, and cleans up.
func (sl *StatsListener) Close() {
	sl.listener.Close()
	sl.wg.Wait()
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

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)

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

	for scanner.Scan() {
		line := scanner.Bytes()
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
			// PUT body follows on the next non-empty line as base64.
			// Body must be read synchronously before the next request.
			// Go <=1.24: JSON string literal (quoted base64)
			// Go >=1.25: raw base64 (unquoted)
			if req.BodySize > 0 {
				for scanner.Scan() {
					bodyLine := scanner.Bytes()
					if len(bodyLine) == 0 {
						continue
					}
					// Body line is base64, optionally wrapped in JSON quotes.
					raw := string(bodyLine)
					if bodyLine[0] == '"' {
						// JSON string: unquote first, then decode base64.
						var unquoted string
						if err := json.Unmarshal(bodyLine, &unquoted); err == nil {
							raw = unquoted
						}
					}
					if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil {
						req.Body = decoded
					}
					break
				}
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
	return scanner.Err()
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

// flushLatency sends a final latency snapshot over the stats socket.
// Pool usage is read from the WebBackend directly (it's shared across
// all daemon Servers, so only the cumulative snapshot matters).
func (s *Server) flushLatency() {
	snap := s.Latency.Snapshot()
	if wb := s.webBackend(); wb != nil {
		snap.Pool = wb.Pool.Snapshot()
	}
	s.sendStat(StatEvent{Latency: &snap})
}

// webBackend extracts the *WebBackend from the remote, unwrapping
// the noCloseBackend wrapper if present.
func (s *Server) webBackend() *WebBackend {
	if s.remote == nil {
		return nil
	}
	if wb, ok := s.remote.(*WebBackend); ok {
		return wb
	}
	if nc, ok := s.remote.(*noCloseBackend); ok {
		if wb, ok := nc.IBackend.(*WebBackend); ok {
			return wb
		}
	}
	return nil
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
			fmt.Fprintf(os.Stderr, "cache: HIT local  %s size=%d\n", actionID, meta.Size)
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
		// Log all misses to a temp file for post-run analysis.
		s.logMiss(actionID)
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
	if s.debug {
		fmt.Fprintf(os.Stderr, "cache: HIT remote %s\n", actionID)
	}
	defer body.Close()

	// Write to local cache for future hits.
	localPutStart := time.Now()
	diskPath, err := s.local.Put(actionID, outputID, body)
	s.Latency.LocalPut.Record(time.Since(localPutStart))

	if err != nil {
		return Response{ID: req.ID, Miss: true}
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
			fmt.Fprintf(os.Stderr, "cache: PUT  dedup  %s\n", actionID)
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
		fmt.Fprintf(os.Stderr, "cache: PUT  new    %s size=%d\n", actionID, len(req.Body))
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
			s.putSem <- struct{}{}        // acquire
			s.Latency.SemWait.Record(time.Since(semStart))
			defer func() { <-s.putSem }() // release
			remotePutStart := time.Now()
			err := s.remote.Put(actionID, outputID, bytes.NewReader(data), int64(len(data)))
			s.Latency.RemotePut.Record(time.Since(remotePutStart))
			if err != nil {
				fmt.Fprintf(os.Stderr, "cacheprog: remote put: %v\n", err)
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
