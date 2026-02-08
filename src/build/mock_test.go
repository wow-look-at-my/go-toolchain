package build

import "fmt"

type MockCommandRunner struct {
	Commands  []mockCommand
	Responses map[string]mockResponse
}

type mockCommand struct {
	Name string
	Args []string
}

type mockResponse struct {
	Output []byte
	Err    error
}

func NewMockRunner() *MockCommandRunner {
	return &MockCommandRunner{Responses: make(map[string]mockResponse)}
}

func (m *MockCommandRunner) key(name string, args ...string) string {
	return fmt.Sprintf("%s %v", name, args)
}

func (m *MockCommandRunner) SetResponse(name string, args []string, output []byte, err error) {
	m.Responses[m.key(name, args...)] = mockResponse{Output: output, Err: err}
}

func (m *MockCommandRunner) Run(name string, args ...string) error {
	m.Commands = append(m.Commands, mockCommand{Name: name, Args: args})
	if resp, ok := m.Responses[m.key(name, args...)]; ok {
		return resp.Err
	}
	return nil
}

func (m *MockCommandRunner) RunWithOutput(name string, args ...string) ([]byte, error) {
	m.Commands = append(m.Commands, mockCommand{Name: name, Args: args})
	if resp, ok := m.Responses[m.key(name, args...)]; ok {
		return resp.Output, resp.Err
	}
	return nil, nil
}
