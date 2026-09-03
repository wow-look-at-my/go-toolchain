package runner

import (
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/wow-look-at-my/go-toolchain/src/hostos"

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

// WithHostTarget builds for the machine this runs on: nothing can fork/exec
// the fork's default fat APE. Depth: docs/CI.md
func (c *Config) WithHostTarget() *Config {
	return c.WithEnv("GOOS", hostos.GOOS()).WithEnv("GOARCH", runtime.GOARCH)
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

	p := &process{cmd: cmd, stdoutPipe: stdout, stderrPipe: stderr, quiet: cfg.Quiet, onFirst: cfg.OnFirstOutput, stdoutWriter: cfg.StdoutWriter, stderrWriter: cfg.StderrWriter}
	return p, nil
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
	stderrWriter io.Writer
}

func (p *process) Wait() error {
	if p.done {
		return p.err
	}
	if !p.quiet {
		// Copy stdout/stderr concurrently so stderr (e.g. "go: downloading...") streams live instead of buffering.
		var stdoutTarget io.Writer = os.Stdout
		if p.stdoutWriter != nil {
			stdoutTarget = p.stdoutWriter
		}
		var stderrTarget io.Writer = os.Stderr
		if p.stderrWriter != nil {
			stderrTarget = p.stderrWriter
		}
		w := &firstOutputWriter{
			target:    stdoutTarget,
			hadOutput: &p.hadOutput,
			callback:  p.onFirst,
		}
		wErr := &firstOutputWriter{
			target:    stderrTarget,
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
