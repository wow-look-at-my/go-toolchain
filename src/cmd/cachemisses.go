package cmd

import (
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// cacheMissTracker captures package import paths from go build -v / go test -v
// stderr output. Each line printed by -v is a package that was compiled because
// it wasn't in the build cache.
type cacheMissTracker struct {
	mu     sync.Mutex
	target io.Writer // underlying writer (e.g. os.Stderr)
	buf    []byte
	pkgs   []string
	seen   map[string]bool
	phase  string // current phase label (e.g. "vet", "test", "build")
}

// newCacheMissTracker creates a tracker that tees to the given writer.
func newCacheMissTracker(target io.Writer) *cacheMissTracker {
	return &cacheMissTracker{
		target: target,
		seen:   make(map[string]bool),
	}
}

// SetPhase labels subsequent misses with a phase name.
func (t *cacheMissTracker) SetPhase(phase string) {
	t.mu.Lock()
	t.phase = phase
	t.mu.Unlock()
}

// Write passes through to the underlying writer and captures lines that look
// like Go package import paths (go build -v output).
func (t *cacheMissTracker) Write(p []byte) (int, error) {
	t.mu.Lock()
	t.buf = append(t.buf, p...)
	for {
		idx := indexOf(t.buf, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimSpace(string(t.buf[:idx]))
		t.buf = t.buf[idx+1:]
		// go build -v prints bare import paths like "encoding/json" or
		// "github.com/foo/bar". Filter: must contain "/", no spaces, no colons.
		if line != "" && strings.Contains(line, "/") && !strings.Contains(line, " ") && !strings.Contains(line, ":") && !strings.HasPrefix(line, "#") {
			if !t.seen[line] {
				t.seen[line] = true
				t.pkgs = append(t.pkgs, line)
			}
		}
	}
	t.mu.Unlock()
	return t.target.Write(p)
}

func indexOf(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}

// Print writes the cache miss summary to stderr.
func (t *cacheMissTracker) Print() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.pkgs) == 0 {
		return
	}
	sort.Strings(t.pkgs)
	logger.Info("\n⇒ Cache misses: %d packages compiled", len(t.pkgs))
	for _, pkg := range t.pkgs {
		logger.Info("    %s", pkg)
	}
}

// activeMissTracker is the global tracker, set when --cache-misses is enabled.
var activeMissTracker *cacheMissTracker
