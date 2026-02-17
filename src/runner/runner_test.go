package runner

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfigIsCmd(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		args     []string
		expected bool
	}{
		{
			name:     "exact match",
			cfg:      Config{Name: "go", Args: []string{"test"}},
			args:     []string{"go", "test"},
			expected: true,
		},
		{
			name:     "prefix match",
			cfg:      Config{Name: "go", Args: []string{"test", "-v", "./..."}},
			args:     []string{"go", "test"},
			expected: true,
		},
		{
			name:     "name only",
			cfg:      Config{Name: "go", Args: []string{"build"}},
			args:     []string{"go"},
			expected: true,
		},
		{
			name:     "wrong name",
			cfg:      Config{Name: "git", Args: []string{"status"}},
			args:     []string{"go"},
			expected: false,
		},
		{
			name:     "wrong subcommand",
			cfg:      Config{Name: "go", Args: []string{"build"}},
			args:     []string{"go", "test"},
			expected: false,
		},
		{
			name:     "more args than config has",
			cfg:      Config{Name: "go", Args: []string{"test"}},
			args:     []string{"go", "test", "-v"},
			expected: false,
		},
		{
			name:     "empty args in check",
			cfg:      Config{Name: "go", Args: []string{}},
			args:     []string{"go"},
			expected: true,
		},
		{
			name:     "multi-level prefix",
			cfg:      Config{Name: "go", Args: []string{"mod", "tidy", "-v"}},
			args:     []string{"go", "mod", "tidy"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.cfg.IsCmd(tt.args[0], tt.args[1:]...)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConfigHasArg(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		args     []string
		expected bool
	}{
		{
			name:     "has single arg",
			cfg:      Config{Name: "go", Args: []string{"test", "-v", "-bench", "."}},
			args:     []string{"-bench"},
			expected: true,
		},
		{
			name:     "has one of multiple",
			cfg:      Config{Name: "go", Args: []string{"test", "-v"}},
			args:     []string{"-bench", "-v", "--verbose"},
			expected: true,
		},
		{
			name:     "has none",
			cfg:      Config{Name: "go", Args: []string{"test", "-v"}},
			args:     []string{"-bench", "-cover"},
			expected: false,
		},
		{
			name:     "empty args to check",
			cfg:      Config{Name: "go", Args: []string{"test", "-v"}},
			args:     []string{},
			expected: false,
		},
		{
			name:     "empty config args",
			cfg:      Config{Name: "go", Args: []string{}},
			args:     []string{"-v"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.cfg.HasArg(tt.args...)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCmd(t *testing.T) {
	cfg := Cmd("go", "test", "-v")
	assert.Equal(t, "go", cfg.Name)
	assert.Equal(t, []string{"test", "-v"}, cfg.Args)
	assert.Nil(t, cfg.Env)
	assert.False(t, cfg.Quiet)
}

func TestConfigWithEnv(t *testing.T) {
	cfg := Cmd("go", "build").WithEnv("GOOS", "linux").WithEnv("GOARCH", "amd64")
	assert.Equal(t, "linux", cfg.Env["GOOS"])
	assert.Equal(t, "amd64", cfg.Env["GOARCH"])
}

func TestConfigWithQuiet(t *testing.T) {
	cfg := Cmd("go", "build").WithQuiet()
	assert.True(t, cfg.Quiet)
}

func TestNewMock(t *testing.T) {
	mock := NewMock()
	assert.NotNil(t, mock)
	assert.Empty(t, mock.Calls())
}

func TestMockSetResponse(t *testing.T) {
	mock := NewMock()
	mock.SetResponse("go", []string{"version"}, []byte("go1.21"), nil)

	proc, err := mock.Run(Config{Name: "go", Args: []string{"version"}})
	assert.Nil(t, err)
	assert.NotNil(t, proc)

	// Read stdout
	buf := make([]byte, 100)
	n, _ := proc.Stdout().Read(buf)
	assert.Equal(t, "go1.21", string(buf[:n]))
}

func TestMockSetStderr(t *testing.T) {
	mock := NewMock()
	mock.SetResponse("go", []string{"build"}, nil, nil)
	mock.SetStderr("go", []string{"build"}, []byte("some warning"))

	proc, err := mock.Run(Config{Name: "go", Args: []string{"build"}})
	assert.Nil(t, err)

	buf := make([]byte, 100)
	n, _ := proc.Stderr().Read(buf)
	assert.Equal(t, "some warning", string(buf[:n]))
}

func TestMockCalls(t *testing.T) {
	mock := NewMock()

	mock.Run(Config{Name: "go", Args: []string{"mod", "tidy"}})
	mock.Run(Config{Name: "go", Args: []string{"test"}})

	calls := mock.Calls()
	assert.Len(t, calls, 2)
	assert.True(t, calls[0].IsCmd("go", "mod", "tidy"))
	assert.True(t, calls[1].IsCmd("go", "test"))
}

func TestMockHandler(t *testing.T) {
	mock := NewMock()
	mock.Handler = func(cfg Config) (IProcess, error) {
		if cfg.IsCmd("go", "test") {
			return MockProcess([]byte("test output"), nil), nil
		}
		return nil, nil // fall through to default
	}

	proc, err := mock.Run(Config{Name: "go", Args: []string{"test"}})
	assert.Nil(t, err)

	buf := make([]byte, 100)
	n, _ := proc.Stdout().Read(buf)
	assert.Equal(t, "test output", string(buf[:n]))
}

func TestMockHandlerFallthrough(t *testing.T) {
	mock := NewMock()
	mock.SetResponse("go", []string{"build"}, []byte("build output"), nil)
	mock.Handler = func(cfg Config) (IProcess, error) {
		// Only handle test, let build fall through
		if cfg.IsCmd("go", "test") {
			return MockProcess([]byte("test output"), nil), nil
		}
		return nil, nil
	}

	// Test should use handler
	proc1, _ := mock.Run(Config{Name: "go", Args: []string{"test"}})
	buf := make([]byte, 100)
	n, _ := proc1.Stdout().Read(buf)
	assert.Equal(t, "test output", string(buf[:n]))

	// Build should fall through to SetResponse
	proc2, _ := mock.Run(Config{Name: "go", Args: []string{"build"}})
	buf2 := make([]byte, 100)
	n2, _ := proc2.Stdout().Read(buf2)
	assert.Equal(t, "build output", string(buf2[:n2]))
}

func TestMockProcessWait(t *testing.T) {
	proc := MockProcess([]byte("output"), nil)
	err := proc.Wait()
	assert.Nil(t, err)

	// Second wait should also work
	err = proc.Wait()
	assert.Nil(t, err)
}

func TestMockProcessWaitError(t *testing.T) {
	proc := MockProcess(nil, assert.AnError)
	err := proc.Wait()
	assert.Equal(t, assert.AnError, err)
}

func TestRealRunnerEcho(t *testing.T) {
	r := New()
	proc, err := r.Run(Config{Name: "echo", Args: []string{"hello", "world"}, Quiet: true})
	assert.Nil(t, err)

	buf := make([]byte, 100)
	n, _ := proc.Stdout().Read(buf)
	assert.Contains(t, string(buf[:n]), "hello world")

	err = proc.Wait()
	assert.Nil(t, err)
}

func TestRealRunnerWithEnv(t *testing.T) {
	r := New()
	proc, err := r.Run(Config{
		Name:  "sh",
		Args:  []string{"-c", "echo $TEST_VAR"},
		Env:   map[string]string{"TEST_VAR": "test_value"},
		Quiet: true,
	})
	assert.Nil(t, err)

	buf := make([]byte, 100)
	n, _ := proc.Stdout().Read(buf)
	assert.Contains(t, string(buf[:n]), "test_value")

	proc.Wait()
}

func TestRealRunnerStderr(t *testing.T) {
	r := New()
	proc, err := r.Run(Config{
		Name:  "sh",
		Args:  []string{"-c", "echo error >&2"},
		Quiet: true,
	})
	assert.Nil(t, err)

	buf := make([]byte, 100)
	n, _ := proc.Stderr().Read(buf)
	assert.Contains(t, string(buf[:n]), "error")

	proc.Wait()
}

func TestRealRunnerFailingCommand(t *testing.T) {
	r := New()
	proc, err := r.Run(Config{Name: "false", Quiet: true})
	assert.Nil(t, err) // Start succeeds

	err = proc.Wait()
	assert.NotNil(t, err) // But wait returns error
}

func TestRealRunnerCommandNotFound(t *testing.T) {
	r := New()
	_, err := r.Run(Config{Name: "nonexistent_command_12345"})
	assert.NotNil(t, err)
}

func TestConfigRun(t *testing.T) {
	mock := NewMock()
	mock.SetResponse("echo", []string{"test"}, []byte("test\n"), nil)

	cfg := Cmd("echo", "test")
	proc, err := cfg.Run(mock)
	assert.Nil(t, err)
	assert.NotNil(t, proc)
}
