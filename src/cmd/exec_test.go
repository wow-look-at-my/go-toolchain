package cmd

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"testing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	assert.Nil(t, err)

	// Test RunWithOutput
	out, err := r.RunWithOutput("echo", "hello")
	assert.Nil(t, err)
	assert.Equal(t, "hello\n", string(out))

	// Test RunWithPipes
	stdout, wait, err := r.RunWithPipes("echo", "piped")
	assert.Nil(t, err)
	data, _ := io.ReadAll(stdout)
	if err := wait(); err != nil {
		t.Errorf("wait failed: %v", err)
	}
	assert.Equal(t, "piped\n", string(data))
}

func TestRealCommandRunnerNonQuiet(t *testing.T) {
	r := &RealCommandRunner{Quiet: false}
	err := r.Run("true")
	assert.Nil(t, err)
}

func TestRealCommandRunnerRunFails(t *testing.T) {
	r := &RealCommandRunner{Quiet: true}
	err := r.Run("false")
	assert.NotNil(t, err)
}

func TestRealCommandRunnerRunWithOutputFails(t *testing.T) {
	r := &RealCommandRunner{Quiet: true}
	_, err := r.RunWithOutput("false")
	assert.NotNil(t, err)
}

func TestRealCommandRunnerRunWithPipesFails(t *testing.T) {
	r := &RealCommandRunner{Quiet: true}
	stdout, wait, err := r.RunWithPipes("sh", "-c", "echo hello && exit 1")
	require.Nil(t, err)
	// Read stdout
	_, _ = io.ReadAll(stdout)
	// Wait should return error
	err = wait()
	assert.NotNil(t, err)
}

func TestMockCommandRunner(t *testing.T) {
	m := NewMockRunner()
	m.SetResponse("test", []string{"arg1"}, []byte("output"), nil)

	out, err := m.RunWithOutput("test", "arg1")
	assert.Nil(t, err)
	assert.Equal(t, "output", string(out))

	assert.Equal(t, 1, len(m.Commands))
	assert.Equal(t, "test", m.Commands[0].Name)
}
