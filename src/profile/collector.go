// Package profile builds the per-action build profile: it injects
// -debug-actiongraph dumps into the go build/test invocations of a run,
// parses them defensively, joins each action (by its 20-char truncated
// ActionID) with the cacheprog's per-action outcome events, and emits a
// console summary, build/profile.json, Chrome-trace lanes, and a CI
// step-summary table — answering "what is this build spending its time on,
// and did the cache help?".
package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// Collector accumulates the -debug-actiongraph dump files produced by the go
// build/test invocations of one go-toolchain run. Safe for concurrent use
// (matrix builds request dump paths from parallel workers).
type Collector struct {
	mu    sync.Mutex
	dir   string
	seq   int
	files []string
}

// NewCollector returns a collector that places actiongraph dumps in dir.
func NewCollector(dir string) *Collector {
	return &Collector{dir: dir}
}

// GraphArg reserves a fresh actiongraph dump path and returns the go-command
// flag that writes it ("-debug-actiongraph=<path>"), or "" when the dump
// directory cannot be created (profiling silently off for this invocation).
func (c *Collector) GraphArg() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return ""
	}
	c.seq++
	path := filepath.Join(c.dir, fmt.Sprintf("actiongraph-%d-%d.json", os.Getpid(), c.seq))
	// Drop any stale file from a previous run of this pid so a go invocation
	// that fails before dumping can never join last run's graph.
	os.Remove(path)
	c.files = append(c.files, path)
	return "-debug-actiongraph=" + path
}

// Files returns a copy of the dump paths handed out so far.
func (c *Collector) Files() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.files...)
}

// active is the process-wide collector consulted by the argv injection sites
// (src/cmd runBuild, src/test RunTests) via the package-level GraphArg. Nil
// when profiling is disabled (--no-profile, or a command that doesn't build).
var active atomic.Pointer[Collector]

// SetActive installs (or, with nil, clears) the process-wide collector.
func SetActive(c *Collector) { active.Store(c) }

// GraphArg returns the -debug-actiongraph flag for a new go invocation, or ""
// when no collector is active. This is the hook the build/test argv
// constructors call, so they need no plumbing or awareness of profiling state.
func GraphArg() string {
	c := active.Load()
	if c == nil {
		return ""
	}
	return c.GraphArg()
}
