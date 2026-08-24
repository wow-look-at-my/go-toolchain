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

// Collector accumulates -debug-actiongraph dumps from one run's go invocations; safe for concurrent use.
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
	// Drop any stale file from a previous run of this pid, so a failed invocation never joins last run's graph.
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

// active is the process-wide collector the argv injection sites use via GraphArg; nil when profiling is off.
var active atomic.Pointer[Collector]

// SetActive installs (or, with nil, clears) the process-wide collector.
func SetActive(c *Collector) { active.Store(c) }

// GraphArg returns the -debug-actiongraph flag for a new invocation, or "" with no active collector.
func GraphArg() string {
	c := active.Load()
	if c == nil {
		return ""
	}
	return c.GraphArg()
}
