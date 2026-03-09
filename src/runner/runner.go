package runner

import (
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
)

// IProcess represents a running or completed process
type IProcess interface {
	// Wait blocks until the process completes and returns the exit error
	Wait() error
	// Stdout returns captured stout
	Stdout()  io.Reader
	// Stderr returns captured stderr
	Stderr()  io.Reader
}

// Config specifies how to run a command
type Config struct {
	Name          string
	Args          []string
	Env           map[string]string // Merged with current environment
	Quiet         bool              // Don't tee stdout/stderr to console
	OnFirstOutput func()            // Called before the first byte of output is written to console
}

// IsCmd checks if this config runs the given command with the given prefix args.
// e.g., cfg.IsCmd("go", "test") matches "go test ...", cfg.IsCmd("go") matches any go command
func (c *Config) IsCmd(name string, args ...string) bool {
	if c.Name != name {
		return false
	}
	for i, arg := range args {
		if i >= len(c.Args) || c.Args[i] != arg {
			return false
		}
	}
	return true
}

// HasArg checks if any of the given arguments appear anywhere in Args.
// e.g., cfg.HasArg("-bench") or cfg.HasArg("-v", "--verbose")
func (c *Config) HasArg(args ...string) bool {
	for _, a := range c.Args {
		for _, want := range args {
			if a == want {
				return true
			}
		}
	}
	return false
}

// Cmd creates a new Config with the given command and arguments
func Cmd(name string, args ...string) *Config {
	return &Config{Name: name, Args: args}
}

// WithEnv adds an environment variable
func (c *Config) WithEnv(key, value string) *Config {
	if c.Env == nil {
		c.Env = make(map[string]string)
	}
	c.Env[key] = value
	return c
}

// WithQuiet suppresses stdout/stderr tee to console
func (c *Config) WithQuiet() *Config {
	c.Quiet = true
	return c
}

// WithOnFirstOutput sets a callback that is called before the first byte
// of output is written to the console. Useful for progress indicators that
// need to print a newline before subprocess output starts.
func (c *Config) WithOnFirstOutput(f func()) *Config {
	c.OnFirstOutput = f
	return c
}

// Run executes the command using the given runner
func (c *Config) Run(r CommandRunner) (IProcess, error) {
	return r.Run(*c)
}

// CommandRunner abstracts command execution for testing
type CommandRunner interface {
	Run(cfg Config) (IProcess, error)
}

// New creates a runner
func New() CommandRunner {
	return &realRunner{}
}

type realRunner struct{}

func (r *realRunner) Run(cfg Config) (IProcess, error) {
	cmd := exec.Command(cfg.Name, cfg.Args...)

	if len(cfg.Env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range cfg.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	p := &process{cmd: cmd, stdoutPipe: stdout, stderrPipe: stderr, quiet: cfg.Quiet, onFirst: cfg.OnFirstOutput}
	return p, nil
}

// firstOutputWriter wraps a writer and calls a callback before the first write.
type firstOutputWriter struct {
	target    io.Writer
	hadOutput *atomic.Bool
	once      sync.Once
	callback  func()
}

func (w *firstOutputWriter) Write(p []byte) (int, error) {
	if len(p) > 0 {
		w.hadOutput.Store(true)
		if w.callback != nil {
			w.once.Do(w.callback)
		}
	}
	return w.target.Write(p)
}

type process struct {
	cmd        *exec.Cmd
	stdoutPipe io.Reader
	stderrPipe io.Reader
	quiet      bool
	done       bool
	err        error
	hadOutput  atomic.Bool
	onFirst    func()
}

func (p *process) Wait() error {
	if p.done {
		return p.err
	}
	if !p.quiet {
		// Copy stdout and stderr concurrently so that stderr output
		// (e.g. "go: downloading..." from go mod tidy) streams in
		// real-time rather than buffering until stdout closes.
		w := &firstOutputWriter{
			target:    os.Stdout,
			hadOutput: &p.hadOutput,
			callback:  p.onFirst,
		}
		wErr := &firstOutputWriter{
			target:    os.Stderr,
			hadOutput: &p.hadOutput,
			callback:  p.onFirst,
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			io.Copy(w, p.stdoutPipe)
		}()
		go func() {
			defer wg.Done()
			io.Copy(wErr, p.stderrPipe)
		}()
		wg.Wait()
	}
	p.err = p.cmd.Wait()
	p.done = true
	return p.err
}

// HadOutput returns true if the process produced any stdout or stderr output.
// Only meaningful after Wait() has been called.
func HadOutput(proc IProcess) bool {
	if p, ok := proc.(*process); ok {
		return p.hadOutput.Load()
	}
	return false
}

func (p *process) Stdout() io.Reader {
	return p.stdoutPipe
}

func (p *process) Stderr() io.Reader {
	return p.stderrPipe
}
