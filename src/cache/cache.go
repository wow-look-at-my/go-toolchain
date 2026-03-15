package cache

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

// Backend is the interface for a remote cache store.
type Backend interface {
	Get(actionID string) (outputID string, body io.ReadCloser, size int64, t time.Time, miss bool, err error)
	Put(actionID, outputID string, body io.Reader, bodySize int64) error
	Close() error
}

// Server implements the GOCACHEPROG JSON-over-stdio protocol.
type Server struct {
	local  *LocalCache
	remote Backend // nil if no remote backend configured
	mu     sync.Mutex
	locks  map[string]*sync.Mutex
}

// NewServer creates a cache server. remote may be nil for local-only mode.
func NewServer(local *LocalCache, remote Backend) *Server {
	return &Server{
		local:  local,
		remote: remote,
		locks:  make(map[string]*sync.Mutex),
	}
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
			if s.remote != nil {
				s.remote.Close()
			}
			return nil

		case CmdPut:
			// PUT body is on the next line as a base64-encoded JSON string.
			if req.BodySize > 0 && scanner.Scan() {
				bodyLine := scanner.Bytes()
				// The body line is a JSON string (base64 encoded inside quotes).
				var decoded string
				if err := json.Unmarshal(bodyLine, &decoded); err == nil {
					req.Body = []byte(decoded)
				}
			}
			writeResp(s.handlePut(req))

		case CmdGet:
			writeResp(s.handleGet(req))
		}
	}

	// Input closed without explicit close command.
	if s.remote != nil {
		s.remote.Close()
	}
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
		return Response{ID: req.ID, Miss: true}
	}

	outputID, body, _, t, remoteMiss, err := s.remote.Get(actionID)
	if err != nil || remoteMiss {
		return Response{ID: req.ID, Miss: true}
	}
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
	if _, miss := s.local.Get(actionID); !miss {
		return Response{ID: req.ID}
	}

	body := bytes.NewReader(req.Body)

	diskPath, err := s.local.Put(actionID, outputID, body)
	if err != nil {
		return Response{ID: req.ID, Err: err.Error()}
	}

	// Async write to remote.
	if s.remote != nil {
		bodyForRemote := strings.NewReader(string(req.Body))
		go s.remote.Put(actionID, outputID, bodyForRemote, int64(len(req.Body)))
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
