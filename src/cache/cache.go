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
}

// Server implements the GOCACHEPROG JSON-over-stdio protocol.
type Server struct {
	local    *LocalCache
	remote   IBackend // nil if no remote backend configured
	mu       sync.Mutex
	locks    map[string]*sync.Mutex
	wg       sync.WaitGroup // tracks in-flight async remote puts
	Misses   AtomicCounter
	statsConn net.Conn // persistent connection to parent's stats socket
	statsMu   sync.Mutex
}

// NewServer creates a cache server. remote may be nil for local-only mode.
// Connects to the stats socket if GOCACHE_STATS_SOCK is set.
func NewServer(local *LocalCache, remote IBackend) *Server {
	s := &Server{
		local:  local,
		remote: remote,
		locks:  make(map[string]*sync.Mutex),
	}
	if sock := os.Getenv("GOCACHE_STATS_SOCK"); sock != "" {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			s.statsConn = conn
		}
	}
	return s
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
	Local  *CacheStats    `json:"local"`
	Remote *CacheStats    `json:"remote,omitempty"`
	Misses *AtomicCounter `json:"misses"`
}

// GetStats returns pointers to the live cache layer stats.
func (s *Server) GetStats() *ServerStats {
	ss := &ServerStats{
		Local:  &s.local.Stats,
		Misses: &s.Misses,
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
		Local:  &sl.local,
		Misses: &sl.misses,
	}
	if sl.hasRemote.Load() {
		ss.Remote = &sl.remote
	}
	return ss
}

// Run starts the protocol loop, reading requests from r and writing
// responses to w. It blocks until the input stream closes or a close
// command is received.
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

	writeResp := func(resp Response) {
		enc.Encode(resp)
	}

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
			writeResp(Response{ID: req.ID})
			s.wg.Wait() // drain in-flight remote puts
			if s.remote != nil {
				s.remote.Close()
			}
			s.closeStats()
			return nil

		case CmdPut:
			// PUT body follows on the next non-empty line as base64.
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
			writeResp(s.handleGet(req))
		}
	}

	// Input closed without explicit close command — still drain async puts.
	s.wg.Wait()
	if s.remote != nil {
		s.remote.Close()
	}
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

func (s *Server) handleGet(req Request) Response {
	actionID := fmt.Sprintf("%x", req.ActionID)
	mu := s.lock(actionID)
	mu.Lock()
	defer mu.Unlock()

	// Check local cache first.
	meta, miss := s.local.Get(actionID)
	if !miss {
		s.sendStat(StatEvent{LocalHit: 1})
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
		return Response{ID: req.ID, Miss: true}
	}

	outputID, body, _, t, remoteMiss, err := s.remote.Get(actionID)
	if err != nil || remoteMiss {
		s.Misses.Increment()
		s.sendStat(StatEvent{Miss: 1})
		return Response{ID: req.ID, Miss: true}
	}
	s.sendStat(StatEvent{RemoteHit: 1})
	defer body.Close()

	// Write to local cache for future hits.
	diskPath, err := s.local.Put(actionID, outputID, body)
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
	mu.Lock()
	defer mu.Unlock()

	// Check if already cached locally.
	if meta, miss := s.local.Get(actionID); !miss {
		return Response{ID: req.ID, DiskPath: meta.DiskPath, Size: meta.Size}
	}

	body := bytes.NewReader(req.Body)

	diskPath, err := s.local.Put(actionID, outputID, body)
	if err != nil {
		return Response{ID: req.ID, Err: err.Error()}
	}
	s.sendStat(StatEvent{LocalPut: 1})
	// Async write to remote.
	if s.remote != nil {
		data := make([]byte, len(req.Body))
		copy(data, req.Body)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			if err := s.remote.Put(actionID, outputID, bytes.NewReader(data), int64(len(data))); err != nil {
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
