package runner

import (
	"bytes"
	"io"
	"strings"
	"sync"
)

// Mock is a test double for CommandRunner
type Mock struct {
	mu        sync.Mutex
	responses map[string]mockResponse
	calls     []Config
	// Handler is called for each Run if set, allowing custom behavior.
	// If it returns non-nil IProcess, that's used instead of looking up responses.
	Handler func(cfg Config) (IProcess, error)
}

type mockResponse struct {
	stdout []byte
	stderr []byte
	err    error
}

// NewMock creates a new mock runner
func NewMock() *Mock {
	return &Mock{
		responses: make(map[string]mockResponse),
	}
}

// key generates a lookup key from command name and args
func (m *Mock) key(name string, args ...string) string {
	return name + " " + strings.Join(args, " ")
}

// SetResponse configures the mock to return specific output for a command
func (m *Mock) SetResponse(name string, args []string, stdout []byte, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses[m.key(name, args...)] = mockResponse{stdout: stdout, err: err}
}

// SetStderr configures stderr output for a command
func (m *Mock) SetStderr(name string, args []string, stderr []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := m.key(name, args...)
	resp := m.responses[key]
	resp.stderr = stderr
	m.responses[key] = resp
}

// Calls returns all commands that were executed
func (m *Mock) Calls() []Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Config(nil), m.calls...)
}

// Run implements CommandRunner
func (m *Mock) Run(cfg Config) (IProcess, error) {
	m.mu.Lock()
	m.calls = append(m.calls, cfg)
	handler := m.Handler
	resp, ok := m.responses[m.key(cfg.Name, cfg.Args...)]
	m.mu.Unlock()

	// If handler is set, let it handle the command
	if handler != nil {
		if proc, err := handler(cfg); proc != nil || err != nil {
			return proc, err
		}
	}

	if !ok {
		// Default: empty success
		return &mockProcess{}, nil
	}

	if resp.err != nil {
		return &mockProcess{stdout: resp.stdout, stderr: resp.stderr, waitErr: resp.err}, nil
	}

	return &mockProcess{stdout: resp.stdout, stderr: resp.stderr}, nil
}

// MockProcess creates a mock process with the given stdout and wait error
func MockProcess(stdout []byte, waitErr error) IProcess {
	return &mockProcess{stdout: stdout, waitErr: waitErr}
}

type mockProcess struct {
	stdout  []byte
	stderr  []byte
	waitErr error
	waited  bool
}

func (p *mockProcess) Wait() error {
	p.waited = true
	return p.waitErr
}

func (p *mockProcess) Stdout() io.Reader {
	return bytes.NewReader(p.stdout)
}

func (p *mockProcess) Stderr() io.Reader {
	return bytes.NewReader(p.stderr)
}
