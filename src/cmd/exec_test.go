package cmd

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"testing"
)

// MockCommandRunner captures commands for testing
type MockCommandRunner struct {
	mu        sync.Mutex
	Commands  []MockCommand
	Responses map[string]MockResponse
}

type MockCommand struct {
	Name string
	Args []string
}

type MockResponse struct {
	Output []byte
	Err    error
}

func NewMockRunner() *MockCommandRunner {
	return &MockCommandRunner{
		Responses: make(map[string]MockResponse),
	}
}

func (m *MockCommandRunner) key(name string, args ...string) string {
	return fmt.Sprintf("%s %v", name, args)
}

func (m *MockCommandRunner) SetResponse(name string, args []string, output []byte, err error) {
	m.Responses[m.key(name, args...)] = MockResponse{Output: output, Err: err}
}

func (m *MockCommandRunner) Run(name string, args ...string) error {
	m.mu.Lock()
	m.Commands = append(m.Commands, MockCommand{Name: name, Args: args})
	resp, ok := m.Responses[m.key(name, args...)]
	m.mu.Unlock()
	if ok {
		return resp.Err
	}
	return nil
}

func (m *MockCommandRunner) RunWithOutput(name string, args ...string) ([]byte, error) {
	m.mu.Lock()
	m.Commands = append(m.Commands, MockCommand{Name: name, Args: args})
	resp, ok := m.Responses[m.key(name, args...)]
	m.mu.Unlock()
	if ok {
		return resp.Output, resp.Err
	}
	return nil, nil
}

func (m *MockCommandRunner) RunWithPipes(name string, args ...string) (io.Reader, func() error, error) {
	m.mu.Lock()
	m.Commands = append(m.Commands, MockCommand{Name: name, Args: args})
	resp, ok := m.Responses[m.key(name, args...)]
	m.mu.Unlock()
	if ok {
		return bytes.NewReader(resp.Output), func() error { return resp.Err }, nil
	}
	return bytes.NewReader(nil), func() error { return nil }, nil
}

func (m *MockCommandRunner) RunWithEnv(env []string, name string, args ...string) error {
	m.mu.Lock()
	m.Commands = append(m.Commands, MockCommand{Name: name, Args: args})
	resp, ok := m.Responses[m.key(name, args...)]
	m.mu.Unlock()
	if ok {
		return resp.Err
	}
	return nil
}

func TestRealCommandRunner(t *testing.T) {
	r := &RealCommandRunner{Quiet: true}

	// Test Run
	err := r.Run("echo", "hello")
	if err != nil {
		t.Errorf("Run failed: %v", err)
	}

	// Test RunWithOutput
	out, err := r.RunWithOutput("echo", "hello")
	if err != nil {
		t.Errorf("RunWithOutput failed: %v", err)
	}
	if string(out) != "hello\n" {
		t.Errorf("expected 'hello\\n', got %q", out)
	}

	// Test RunWithPipes
	stdout, wait, err := r.RunWithPipes("echo", "piped")
	if err != nil {
		t.Errorf("RunWithPipes failed: %v", err)
	}
	data, _ := io.ReadAll(stdout)
	if err := wait(); err != nil {
		t.Errorf("wait failed: %v", err)
	}
	if string(data) != "piped\n" {
		t.Errorf("expected 'piped\\n', got %q", data)
	}
}

func TestRealCommandRunnerNonQuiet(t *testing.T) {
	r := &RealCommandRunner{Quiet: false}
	err := r.Run("true")
	if err != nil {
		t.Errorf("Run failed: %v", err)
	}
}

func TestRealCommandRunnerRunFails(t *testing.T) {
	r := &RealCommandRunner{Quiet: true}
	err := r.Run("false")
	if err == nil {
		t.Error("expected error for failing command")
	}
}

func TestRealCommandRunnerRunWithOutputFails(t *testing.T) {
	r := &RealCommandRunner{Quiet: true}
	_, err := r.RunWithOutput("false")
	if err == nil {
		t.Error("expected error for failing command")
	}
}

func TestRealCommandRunnerRunWithPipesFails(t *testing.T) {
	r := &RealCommandRunner{Quiet: true}
	stdout, wait, err := r.RunWithPipes("sh", "-c", "echo hello && exit 1")
	if err != nil {
		t.Fatalf("RunWithPipes start failed: %v", err)
	}
	// Read stdout
	_, _ = io.ReadAll(stdout)
	// Wait should return error
	err = wait()
	if err == nil {
		t.Error("expected error for failing command")
	}
}

func TestMockCommandRunner(t *testing.T) {
	m := NewMockRunner()
	m.SetResponse("test", []string{"arg1"}, []byte("output"), nil)

	out, err := m.RunWithOutput("test", "arg1")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if string(out) != "output" {
		t.Errorf("expected 'output', got %q", out)
	}

	if len(m.Commands) != 1 {
		t.Errorf("expected 1 command, got %d", len(m.Commands))
	}
	if m.Commands[0].Name != "test" {
		t.Errorf("expected command 'test', got %q", m.Commands[0].Name)
	}
}
