// Package profile builds the per-action build profile: it injects
// -debug-actiongraph dumps into the go build/test invocations of a run,
// parses them defensively, joins each action (by its truncated
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

// Collector accumulates -debug-actiongraph dumps and -debug-trace spans from a run's go invocations; safe for concurrent use.
type Collector struct {
	mu     sync.Mutex
	dir    string
	seq    int
	traces int
	files  []string
	spans  []string
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

// TraceArg reserves a fresh span file and returns the go-command flag that
// writes it ("-debug-trace=<path>"), or "" when the directory cannot be
// created. The go command spans what it does internally; go-toolchain only
// knows that the command ran and how long it took, so these files are the
// only record of where the time inside a go invocation went.
func (c *Collector) TraceArg() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return ""
	}
	c.traces++
	path := filepath.Join(c.dir, fmt.Sprintf("gotrace-%d-%d.json", os.Getpid(), c.traces))
	// Drop any stale file from a previous run of this pid, so an invocation never appends to last run's spans.
	os.Remove(path)
	c.spans = append(c.spans, path)
	return "-debug-trace=" + path
}

// Files returns a copy of the dump paths handed out so far.
func (c *Collector) Files() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.files...)
}

// Spans returns a copy of the -debug-trace paths handed out so far.
func (c *Collector) Spans() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.spans...)
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

// TraceArg returns the -debug-trace flag for a new invocation, or "" with no active collector.
func TraceArg() string {
	c := active.Load()
	if c == nil {
		return ""
	}
	return c.TraceArg()
}
