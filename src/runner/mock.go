package runner

import (
	"bytes"
	"io"
	"strings"
)

// Mock is a test double for CommandRunner
type Mock struct {
	responses map[string]mockResponse
	calls     []Config
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
	m.responses[m.key(name, args...)] = mockResponse{stdout: stdout, err: err}
}

// SetStderr configures stderr output for a command
func (m *Mock) SetStderr(name string, args []string, stderr []byte) {
	key := m.key(name, args...)
	resp := m.responses[key]
	resp.stderr = stderr
	m.responses[key] = resp
}

// Calls returns all commands that were executed
func (m *Mock) Calls() []Config {
	return m.calls
}

// Run implements CommandRunner
func (m *Mock) Run(cfg Config) (IProcess, error) {
	m.calls = append(m.calls, cfg)

	resp, ok := m.responses[m.key(cfg.Name, cfg.Args...)]
	if !ok {
		// Default: empty success
		return &mockProcess{}, nil
	}

	if resp.err != nil {
		return &mockProcess{stdout: resp.stdout, stderr: resp.stderr, waitErr: resp.err}, nil
	}

	return &mockProcess{stdout: resp.stdout, stderr: resp.stderr}, nil
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
