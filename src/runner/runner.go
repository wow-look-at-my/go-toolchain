package runner

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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

type realRunner struct {
	grace time.Duration // zero means defaultDrainGrace
}

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

	// Ours, not StdoutPipe's: exec closes those at Wait, and reap outlives it.
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		stdoutR.Close()
		stdoutW.Close()
		return nil, err
	}
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	if err := cmd.Start(); err != nil {
		for _, f := range []*os.File{stdoutR, stdoutW, stderrR, stderrW} {
			f.Close()
		}
		return nil, err
	}
	// The child holds the only write ends now. A reader gets no EOF until we drop ours.
	stdoutW.Close()
	stderrW.Close()

	grace := r.grace
	if grace == 0 {
		grace = defaultDrainGrace
	}
	outR, outW := io.Pipe()
	errR, errW := io.Pipe()
	p := &process{cmd: cmd, stdoutPipe: outR, stderrPipe: errR, quiet: cfg.Quiet, onFirst: cfg.OnFirstOutput, stdoutWriter: cfg.StdoutWriter, stderrWriter: cfg.StderrWriter, exited: make(chan struct{}), grace: grace}
	go relay(stdoutR, outW)
	go relay(stderrR, errW)
	go p.reap(outW, errW)
	return p, nil
}

// relay carries the OS pipe into the io.Pipe a caller reads, and ends it on EOF.
func relay(src *os.File, dst *io.PipeWriter) {
	_, err := io.Copy(dst, src)
	dst.CloseWithError(err)
}

// defaultDrainGrace is how long a relay runs on after the command exits.
const defaultDrainGrace = 5 * time.Second

// reap ends any read still waiting on EOF once the command is gone.
// A grandchild holding the child's stdout keeps the OS pipe open, and that
// read blocks inside a syscall no deadline or close can interrupt. So the
// bound goes on the io.Pipe above it, and the relay stays parked.
func (p *process) reap(writers ...*io.PipeWriter) {
	p.waitErr = p.cmd.Wait()
	close(p.exited)
	time.Sleep(p.grace)
	for _, w := range writers {
		w.Close()
	}
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
	stdoutPipe   *io.PipeReader
	stderrPipe   *io.PipeReader
	quiet        bool
	done         bool
	err          error
	hadOutput    atomic.Bool
	onFirst      func()
	stdoutWriter io.Writer
	stderrWriter io.Writer
	exited       chan struct{} // closed when reap has the exit status
	waitErr      error         // read only after exited is closed
	grace        time.Duration
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
	<-p.exited
	p.err = p.waitErr
	p.done = true
	p.stdoutPipe.Close()
	p.stderrPipe.Close()
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
