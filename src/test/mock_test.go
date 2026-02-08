package test

import (
	"bytes"
	"fmt"
	"io"
)

type MockCommandRunner struct {
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
	return &MockCommandRunner{Responses: make(map[string]MockResponse)}
}

func (m *MockCommandRunner) key(name string, args ...string) string {
	return fmt.Sprintf("%s %v", name, args)
}

func (m *MockCommandRunner) SetResponse(name string, args []string, output []byte, err error) {
	m.Responses[m.key(name, args...)] = MockResponse{Output: output, Err: err}
}

func (m *MockCommandRunner) Run(name string, args ...string) error {
	m.Commands = append(m.Commands, MockCommand{Name: name, Args: args})
	if resp, ok := m.Responses[m.key(name, args...)]; ok {
		return resp.Err
	}
	return nil
}

func (m *MockCommandRunner) RunWithOutput(name string, args ...string) ([]byte, error) {
	m.Commands = append(m.Commands, MockCommand{Name: name, Args: args})
	if resp, ok := m.Responses[m.key(name, args...)]; ok {
		return resp.Output, resp.Err
	}
	return nil, nil
}

func (m *MockCommandRunner) RunWithPipes(name string, args ...string) (io.Reader, func() error, error) {
	m.Commands = append(m.Commands, MockCommand{Name: name, Args: args})
	if resp, ok := m.Responses[m.key(name, args...)]; ok {
		return bytes.NewReader(resp.Output), func() error { return resp.Err }, nil
	}
	return bytes.NewReader(nil), func() error { return nil }, nil
}
