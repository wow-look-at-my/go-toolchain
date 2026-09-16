package runner

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-containers/sortedmap"
)

// IProcess represents a running or completed process
type IProcess interface {
	// Wait blocks until the process completes and returns the exit error
	Wait() error
	// Stdout returns captured stout
	Stdout() io.Reader
	// Stderr returns captured stderr
	Stderr() io.Reader
}

// Config specifies how to run a command
type Config struct {
	Name          string
	Args          []string
	Dir           string                               // Working directory; empty means the caller's own
	Env           *sortedmap.SortedMap[string, string] // Merged with current environment
	Quiet         bool                                 // Don't tee stdout/stderr to console
	OnFirstOutput func()                               // Called before any output byte is written to console
	StdoutWriter  io.Writer                            // If set, stdout is copied here instead of os.Stdout
	StderrWriter  io.Writer                            // If set, stderr is copied here instead of os.Stderr
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
		c.Env = sortedmap.New[string, string]()
	}
	c.Env.Put(key, value)
	return c
}

// WithDir runs the command in dir, so a caller need not move the process.
func (c *Config) WithDir(dir string) *Config {
	c.Dir = dir
	return c
}

// WithQuiet suppresses stdout/stderr tee to console
func (c *Config) WithQuiet() *Config {
	c.Quiet = true
	return c
}

// WithOnFirstOutput sets a callback invoked before any output byte, for progress indicators.
func (c *Config) WithOnFirstOutput(f func()) *Config {
	c.OnFirstOutput = f
	return c
}

// WithStderrWriter sets a custom writer for stderr output.
func (c *Config) WithStderrWriter(w io.Writer) *Config {
	c.StderrWriter = w
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
	cmd.Dir = cfg.Dir

	if cfg.Env != nil && cfg.Env.Len() > 0 {
		// Merge overrides into the environment, dropping overridden keys up front: a duplicate key's platform behavior varies.
		overrides := set.New[string](cfg.Env.Len())
		for k := range cfg.Env.All() {
			overrides.Add(k)
		}
		for _, e := range os.Environ() {
			if k, _, ok := strings.Cut(e, "="); ok && overrides.Contains(k) {
				continue // skip — will be replaced by override
			}
			cmd.Env = append(cmd.Env, e)
		}
		for k, v := range cfg.Env.All() {
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

	held := newBufferedPipe()
	p := &process{cmd: cmd, stdoutPipe: stdout, stderrPipe: held, quiet: cfg.Quiet, onFirst: cfg.OnFirstOutput, stdoutWriter: cfg.StdoutWriter, stderrDrained: make(chan struct{})}

	// The console still sees each line; held keeps a copy for Stderr.
	var sink io.Writer = held
	if live := cfg.liveStderr(); live != nil {
		sink = io.MultiWriter(&firstOutputWriter{target: live, hadOutput: &p.hadOutput, callback: cfg.OnFirstOutput}, held)
	}
	// Draining here stops a full stderr pipe wedging the child.
	go func() {
		defer close(p.stderrDrained)
		defer held.Close()
		io.Copy(sink, stderr)
	}()
	return p, nil
}

// liveStderr names where stderr goes as it arrives, or nil to only hold it.
func (c *Config) liveStderr() io.Writer {
	switch {
	case c.StderrWriter != nil:
		return c.StderrWriter
	case !c.Quiet:
		return os.Stderr
	}
	return nil
}

// bufferedPipe accepts a write without blocking and serves it to a reader.
type bufferedPipe struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    bytes.Buffer
	closed bool
}

func newBufferedPipe() *bufferedPipe {
	b := &bufferedPipe{}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *bufferedPipe) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.buf.Write(p)
	b.cond.Broadcast()
	return n, err
}

// Close reports EOF to a reader that has taken everything written.
func (b *bufferedPipe) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	b.cond.Broadcast()
	return nil
}

func (b *bufferedPipe) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for b.buf.Len() == 0 && !b.closed {
		b.cond.Wait()
	}
	if b.buf.Len() == 0 {
		return 0, io.EOF
	}
	return b.buf.Read(p)
}

// firstOutputWriter wraps a writer and calls a callback before any write.
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
	cmd          *exec.Cmd
	stdoutPipe   io.Reader
	stderrPipe   io.Reader
	quiet        bool
	done         bool
	err          error
	hadOutput    atomic.Bool
	onFirst      func()
	stdoutWriter io.Writer
	// Closed when Run's stderr drain reaches EOF.
	stderrDrained chan struct{}
}

func (p *process) Wait() error {
	if p.done {
		return p.err
	}
	if !p.quiet {
		// Stderr already went to its target as it arrived, so only stdout is left.
		var stdoutTarget io.Writer = os.Stdout
		if p.stdoutWriter != nil {
			stdoutTarget = p.stdoutWriter
		}
		w := &firstOutputWriter{
			target:    stdoutTarget,
			hadOutput: &p.hadOutput,
			callback:  p.onFirst,
		}
		io.Copy(w, p.stdoutPipe)
	}
	<-p.stderrDrained
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
